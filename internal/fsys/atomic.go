package fsys

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
)

var atomicWriteNonce uint64

// WriteFileAtomic writes data to path atomically using a temp file + rename.
// The temp file is created in the same directory as path to ensure the rename
// is on the same filesystem (required for atomic rename on POSIX). Permissions
// are enforced on the temp file before the rename so the final path is never
// visible with a wider mode (no write-then-chmod window).
func WriteFileAtomic(fs FS, path string, data []byte, perm os.FileMode) error {
	// Timestamp and counter are SEPARATE fields, not summed. Adding them aliases: a call
	// at nanosecond T with counter 1 and a call at T-1 with counter 2 both produce T+1, so
	// two concurrent writers can derive the same temp path. One then renames it away and
	// the other's rename fails with ENOENT — observed as
	// "renaming temp file: ... no such file or directory" under concurrent writers.
	//
	// The counter alone is unique within a process and the pid separates processes, so the
	// timestamp is only there to keep orphaned temps human-readable. parseAtomicTempPID
	// reads the pid up to the FIRST dot, so the extra field does not disturb the sweeper.
	seq := atomic.AddUint64(&atomicWriteNonce, 1)
	suffix := strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(time.Now().UnixNano(), 36) +
		"." + strconv.FormatUint(seq, 36)
	tmp := path + ".tmp." + suffix
	if err := fs.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	// Chmod before rename so the final path never exists with a wider mode
	// even briefly. umask can relax `perm` on the initial WriteFile; an
	// explicit Chmod normalises it.
	if err := fs.Chmod(tmp, perm); err != nil {
		_ = fs.Remove(tmp)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := renameWithTransientRetry(fs, tmp, path); err != nil {
		_ = fs.Remove(tmp)
		return fmt.Errorf("renaming temp file: %w", err)
	}
	sweepDeadAtomicOrphans(fs, path)
	return nil
}

// renameWithTransientRetry renames tmp onto path, retrying when the failure is a
// transient Windows sharing error (ERROR_ACCESS_DENIED / ERROR_SHARING_VIOLATION):
// antivirus scanners, the search indexer, or a concurrent reader can hold the
// destination open, and NTFS refuses the replace while they do. Unix never reports
// these errno values from rename, so the retry loop is Windows-only in practice and
// deterministic errors (including fsys.Fake's) fail on the first try.
//
// The budget is ~4s rather than the original ~255ms because those two sources of
// contention have different shapes. A scanner or indexer holds a file for a few
// milliseconds, which 255ms absorbed easily. APPLICATION readers are different: a
// process polling a sidecar in a loop can lose a replace many races in a row. Sizing
// costs nothing on the common path — the loop returns the instant the rename succeeds —
// so this only changes how much sustained contention a writer survives before failing.
//
// It is not the whole fix, and should not be read as one. OSFS.ReadFile now opens with
// FILE_SHARE_DELETE (see readFileSharing), so a well-behaved reader no longer BLOCKS a
// replace at all; this budget covers what is left, including readers outside this
// package that still open without share-delete.
func renameWithTransientRetry(fs FS, tmp, path string) error {
	delay := time.Millisecond
	for attempt := 0; ; attempt++ {
		err := fs.Rename(tmp, path)
		if err == nil || attempt >= 12 || !isTransientRenameError(err) {
			return err
		}
		time.Sleep(delay)
		delay *= 2 // 1+2+...+2048ms ≈ 4s worst case
	}
}

// sweepDeadAtomicOrphans removes sibling temp files left behind by previous
// WriteFileAtomic callers that died (e.g., SIGTERM) between WriteFile and
// Rename. It is best-effort: any error during enumeration or removal is
// ignored so a stale-temp cleanup never fails an otherwise successful write.
//
// Only siblings of `target` matching the WriteFileAtomic suffix scheme
// (`<basename>.tmp.<pid>.<unixnano-base36>`) are considered. PIDs that are
// still alive — including in-progress writers from concurrent calls — are
// preserved.
func sweepDeadAtomicOrphans(fs FS, target string) {
	dir := filepath.Dir(target)
	prefix := filepath.Base(target) + ".tmp."
	entries, err := fs.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		pid, ok := parseAtomicTempPID(name[len(prefix):])
		if !ok {
			continue
		}
		if pidutil.Alive(pid) {
			continue
		}
		_ = fs.Remove(filepath.Join(dir, name))
	}
}

// parseAtomicTempPID parses the `<pid>.<unixnano-base36>` suffix produced by
// WriteFileAtomic and returns the PID. Returns ok=false when the input does
// not match the scheme (e.g., no dot, non-numeric PID).
func parseAtomicTempPID(suffix string) (int, bool) {
	dot := strings.IndexByte(suffix, '.')
	if dot <= 0 || dot == len(suffix)-1 {
		return 0, false
	}
	pid, err := strconv.Atoi(suffix[:dot])
	if err != nil || pid <= 0 {
		return 0, false
	}
	if suffix[dot+1:] == "" {
		return 0, false
	}
	for _, r := range suffix[dot+1:] {
		if ('0' > r || r > '9') && ('a' > r || r > 'z') {
			return 0, false
		}
	}
	if _, err := strconv.ParseInt(suffix[dot+1:], 36, 64); err != nil {
		return 0, false
	}
	return pid, true
}

// WriteFileIfChangedAtomic writes data to path atomically only when the
// existing on-disk bytes differ. Returns nil with no write when the content
// already matches on a stable regular file. Read or stat errors are ignored
// and the write proceeds — this is a best-effort optimization to avoid
// churning mtime on no-op writes, not a safety check.
func WriteFileIfChangedAtomic(fs FS, path string, data []byte, perm os.FileMode) error {
	if info, err := fs.Lstat(path); err == nil && info.Mode().IsRegular() {
		if snapshot, err := readRegularFileSnapshot(fs, path); err == nil && bytes.Equal(snapshot.data, data) {
			if info, err := fs.Lstat(path); err == nil && info.Mode().IsRegular() {
				if identityStillMatches(fs, path, info, snapshot, data) {
					return nil
				}
			}
		}
	}
	return WriteFileAtomic(fs, path, data, perm)
}

// identityStillMatches reports whether path still names the file captured in
// snapshot. On Unix the re-checked Lstat carries dev/ino; Windows Lstat
// exposes no identity fields, so a second by-handle snapshot supplies the
// identity (and re-confirms the bytes) from the same source as the first.
func identityStillMatches(fs FS, path string, info os.FileInfo, snapshot regularFileSnapshot, data []byte) bool {
	if !snapshot.hasID {
		return false
	}
	if id, ok := fileIdentityFromInfo(info); ok {
		return id == snapshot.id
	}
	second, err := readRegularFileSnapshot(fs, path)
	return err == nil && second.hasID && second.id == snapshot.id && bytes.Equal(second.data, data)
}

// WriteFileIfContentOrModeChangedAtomic writes data to path atomically when
// the existing on-disk bytes, file type, or permissions differ. Returns nil
// with no write when the path is already a regular file with matching content
// and mode. Symlinks and other non-regular entries are replaced without first
// reading through them. Read or stat errors are ignored and the write proceeds.
func WriteFileIfContentOrModeChangedAtomic(fs FS, path string, data []byte, perm os.FileMode) error {
	if info, err := fs.Lstat(path); err == nil && info.Mode().IsRegular() && ComparableMode(info.Mode()) == ComparableMode(perm) {
		if snapshot, err := readRegularFileSnapshot(fs, path); err == nil && bytes.Equal(snapshot.data, data) {
			if info, err := fs.Lstat(path); err == nil && info.Mode().IsRegular() && ComparableMode(info.Mode()) == ComparableMode(perm) {
				if identityStillMatches(fs, path, info, snapshot, data) {
					return nil
				}
			}
		}
	}
	return WriteFileAtomic(fs, path, data, perm)
}

type regularFileSnapshotReader interface {
	readRegularFileSnapshot(name string) (regularFileSnapshot, error)
}

type regularFileSnapshot struct {
	data  []byte
	id    fileIdentity
	hasID bool
}

type fileIdentity struct {
	dev uint64
	ino uint64
}

func readRegularFileSnapshot(fs FS, path string) (regularFileSnapshot, error) {
	if reader, ok := fs.(regularFileSnapshotReader); ok {
		return reader.readRegularFileSnapshot(path)
	}
	return regularFileSnapshot{}, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
}

// ComparableMode returns the portion of a file mode that is significant when
// deciding whether an on-disk file already matches a desired mode: the
// permission bits plus the setuid, setgid, and sticky bits.
func ComparableMode(mode os.FileMode) os.FileMode {
	return mode & (os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
}

func fileIdentityFromInfo(info os.FileInfo) (fileIdentity, bool) {
	return fileIdentityFromSys(info.Sys())
}

func fileIdentityFromSys(sys any) (fileIdentity, bool) {
	// Signed stat fields follow Go's direct int-to-uint conversion so the
	// Fstat and Lstat paths agree on device identity across Unix variants.
	stat := reflect.Indirect(reflect.ValueOf(sys))
	if !stat.IsValid() {
		return fileIdentity{}, false
	}
	dev := stat.FieldByName("Dev")
	ino := stat.FieldByName("Ino")
	if !dev.IsValid() || !ino.IsValid() {
		return fileIdentity{}, false
	}
	devValue, ok := numericFieldToUint64(dev)
	if !ok {
		return fileIdentity{}, false
	}
	inoValue, ok := numericFieldToUint64(ino)
	if !ok {
		return fileIdentity{}, false
	}
	return fileIdentity{dev: devValue, ino: inoValue}, true
}

func numericFieldToUint64(v reflect.Value) (uint64, bool) {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return uint64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint(), true
	default:
		return 0, false
	}
}

// ReadFileWithTransientRetry reads path, retrying briefly on the same transient
// Windows sharing errors that renameWithTransientRetry absorbs on the write side.
//
// It is the read-side counterpart, and it is needed for the same reason. NTFS refuses
// concurrent access while a replace is in flight, so a reader that opens a file at the
// instant WriteFileAtomic swaps it in gets ERROR_SHARING_VIOLATION or
// ERROR_ACCESS_DENIED rather than either the old or the new contents. POSIX has no such
// window — a rename there is atomic to readers — so a read path that is correct on Unix
// can fail intermittently on Windows with no bug in either the reader or the writer.
//
// The retry is bounded and the delays match the rename side. A genuinely missing file
// or a permission error is not transient and returns on the first attempt, so this does
// not mask real failures.
func ReadFileWithTransientRetry(fs FS, path string) ([]byte, error) {
	delay := time.Millisecond
	for attempt := 0; ; attempt++ {
		data, err := fs.ReadFile(path)
		if err == nil || attempt >= 8 || !isTransientRenameError(err) {
			return data, err
		}
		time.Sleep(delay)
		delay *= 2 // 1+2+...+128ms ≈ 255ms worst case, same budget as the rename retry
	}
}
