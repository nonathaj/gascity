//go:build windows

// Package fslock provides small cross-platform advisory file-lock helpers
// (flock(2) on Unix, LockFileEx on Windows) shared by packages that need
// cross-process serialization around a lock file.
package fslock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// All helpers lock the first byte of the file; lock and unlock ranges must
// match, and every caller in this codebase locks whole dedicated lock files,
// so a single-byte range is equivalent to flock semantics.

// LockEx acquires an exclusive lock on f, blocking until available.
func LockEx(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped)
}

// TryLockEx attempts a non-blocking exclusive lock on f. When another process
// holds the lock it returns an error for which WouldBlock reports true.
func TryLockEx(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
}

// Unlock releases a lock acquired by LockEx or TryLockEx.
func Unlock(f *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &overlapped)
}

// WouldBlock reports whether err from TryLockEx means the lock is held
// elsewhere (as opposed to a real failure).
func WouldBlock(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
