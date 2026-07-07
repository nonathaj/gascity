//go:build !windows

// Package fslock provides small cross-platform advisory file-lock helpers
// (flock(2) on Unix, LockFileEx on Windows) shared by packages that need
// cross-process serialization around a lock file.
package fslock

import (
	"errors"
	"os"
	"syscall"
)

// LockEx acquires an exclusive lock on f, blocking until available.
func LockEx(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// TryLockEx attempts a non-blocking exclusive lock on f. When another process
// holds the lock it returns an error for which WouldBlock reports true.
func TryLockEx(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// Unlock releases a lock acquired by LockEx or TryLockEx.
func Unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// WouldBlock reports whether err from TryLockEx means the lock is held
// elsewhere (as opposed to a real failure).
func WouldBlock(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
