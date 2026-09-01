//go:build windows

package beads

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// lockSQLiteSequenceFloorDir takes the exclusive cross-process lock guarding
// dir and returns the release.
//
// Windows offers no equivalent of the Unix directory flock this mirrors:
// LockFileEx needs a byte range in a FILE, and CreateFile on a directory yields
// a handle it refuses. The two remaining options are a lock file inside the
// store directory — ruled out by persistSQLiteSequenceFloorAtLeast, because
// graph migration censuses every entry there — and a named mutex, which is a
// kernel object with no filesystem presence at all. Hence the mutex.
//
// The name is derived from the directory path so two processes serializing on
// the same store meet on the same object. It is hashed rather than embedded:
// mutex names cannot contain a backslash (it is the namespace separator) and
// are capped at MAX_PATH, neither of which a store path respects. The path is
// cleaned and case-folded first because Windows paths are case-insensitive, so
// two spellings of one directory must not produce two different locks.
//
// Local\ scopes the mutex to the session, matching flock's reach: the store is
// a local directory, and a Global\ name would need privileges gc does not ask
// for.
func lockSQLiteSequenceFloorDir(dir string) (func() error, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving sequence-floor lock directory: %w", err)
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	name, err := windows.UTF16PtrFromString(`Local\gc-seqfloor-` + hex.EncodeToString(sum[:16]))
	if err != nil {
		return nil, fmt.Errorf("building sequence-floor lock name: %w", err)
	}
	// CreateMutex returns the existing object when one is already named; the
	// ERROR_ALREADY_EXISTS it sets alongside a valid handle is that report, not
	// a failure, so only a nil handle is an error.
	handle, err := windows.CreateMutex(nil, false, name)
	if handle == 0 {
		return nil, fmt.Errorf("creating SQLite sequence-floor lock: %w", err)
	}
	observeSQLiteSequenceFloorBoundary("sequence-floor-lock-open")

	// WAIT_ABANDONED means a previous holder died without releasing. Ownership
	// still transfers here, so the lock is held either way; the floor file it
	// guards is replaced by rename and cannot be torn, so there is no wreckage
	// to repair before proceeding.
	state, err := windows.WaitForSingleObject(handle, windows.INFINITE)
	if state != uint32(windows.WAIT_OBJECT_0) && state != uint32(windows.WAIT_ABANDONED) {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("locking SQLite sequence floor: wait state %#x: %w", state, err)
	}
	return func() error {
		var releaseErr error
		if err := windows.ReleaseMutex(handle); err != nil {
			releaseErr = fmt.Errorf("unlocking SQLite sequence floor: %w", err)
		}
		observeSQLiteSequenceFloorBoundary("sequence-floor-lock-close-before")
		if err := windows.CloseHandle(handle); err != nil {
			if releaseErr != nil {
				return releaseErr
			}
			return fmt.Errorf("closing SQLite sequence-floor lock handle: %w", err)
		}
		observeSQLiteSequenceFloorBoundary("sequence-floor-lock-close-after")
		return releaseErr
	}, nil
}
