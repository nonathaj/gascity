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

// sequenceFloorDirLockIsFree reports whether the sequence-floor lock on dir
// could be taken right now, without blocking.
//
// It has to mirror lockSQLiteSequenceFloorDir's Windows mechanism rather than
// its Unix one: there is no flock here, and the lock is a named mutex, so the
// only probe that contends with the real holder is opening that same mutex. A
// zero-millisecond wait is the non-blocking try — WAIT_TIMEOUT means held.
func sequenceFloorDirLockIsFree(dir string) (bool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	name, err := windows.UTF16PtrFromString(`Local\gc-seqfloor-` + hex.EncodeToString(sum[:16]))
	if err != nil {
		return false, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if handle == 0 {
		return false, fmt.Errorf("opening sequence-floor lock for probe: %w", err)
	}
	defer windows.CloseHandle(handle) //nolint:errcheck // probe handle

	state, err := windows.WaitForSingleObject(handle, 0)
	switch state {
	case uint32(windows.WAIT_TIMEOUT):
		return false, nil
	case uint32(windows.WAIT_OBJECT_0), uint32(windows.WAIT_ABANDONED):
		_ = windows.ReleaseMutex(handle)
		return true, nil
	default:
		return false, fmt.Errorf("probing sequence-floor lock: wait state %#x: %w", state, err)
	}
}
