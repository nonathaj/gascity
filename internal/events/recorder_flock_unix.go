//go:build !windows

package events

import (
	"errors"
	"os"
	"syscall"
)

// tryLockRecorderFile attempts a non-blocking exclusive cross-process lock on f.
func tryLockRecorderFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// recorderLockWouldBlock reports whether err means another writer holds the lock.
func recorderLockWouldBlock(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

// unlockRecorderFile releases the cross-process lock on f.
func unlockRecorderFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
