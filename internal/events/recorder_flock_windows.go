//go:build windows

package events

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// The recorder locks byte 0 of the log file; LockFileEx/UnlockFileEx must use
// matching ranges. Rotation swaps r.file, and each Record locks/unlocks the
// same *os.File, so the range never leaks across files.

// tryLockRecorderFile attempts a non-blocking exclusive cross-process lock on f.
func tryLockRecorderFile(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

// recorderLockWouldBlock reports whether err means another writer holds the lock.
func recorderLockWouldBlock(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// unlockRecorderFile releases the cross-process lock on f.
func unlockRecorderFile(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
}
