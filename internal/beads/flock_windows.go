//go:build windows

package beads

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// FileFlock implements Locker using LockFileEx on the given path — the
// Windows equivalent of the Unix flock(2) implementation.
// The lock file is created if it does not exist.
type FileFlock struct {
	path string
	f    *os.File
}

// NewFileFlock returns a new FileFlock that locks the given path.
func NewFileFlock(path string) *FileFlock {
	return &FileFlock{path: path}
}

// Lock acquires an exclusive lock, creating the lock file if needed. It
// blocks until the lock is available, matching Unix flock semantics.
func (fl *FileFlock) Lock() error {
	f, err := os.OpenFile(fl.path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("flock open: %w", err)
	}
	var overlapped windows.Overlapped
	if err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped); err != nil {
		_ = f.Close()
		return fmt.Errorf("flock lock: %w", err)
	}
	fl.f = f
	return nil
}

// Unlock releases the lock and closes the lock file.
func (fl *FileFlock) Unlock() error {
	if fl.f == nil {
		return nil
	}
	// Unlock then close; ignore unlock error if close succeeds.
	var overlapped windows.Overlapped
	windows.UnlockFileEx(windows.Handle(fl.f.Fd()), 0, 1, 0, &overlapped) //nolint:errcheck // best-effort unlock before close
	err := fl.f.Close()
	fl.f = nil
	return err
}
