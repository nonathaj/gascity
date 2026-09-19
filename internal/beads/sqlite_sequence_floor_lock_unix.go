//go:build !windows

package beads

import (
	"os"
	"syscall"
)

// sequenceFloorLock is the cross-process lock behind
// persistSQLiteSequenceFloorAtLeast. lock blocks until held; unlock releases
// it; close discards the handle. Each platform file supplies the one shape
// that satisfies the directory-lock rationale documented on that function.
type sequenceFloorLock interface {
	lock() error
	unlock() error
	close() error
}

// dirFlockLock is an flock(2) on the store directory's own descriptor.
type dirFlockLock struct{ f *os.File }

// openSequenceFloorLock opens dir for locking.
func openSequenceFloorLock(dir string) (sequenceFloorLock, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	return dirFlockLock{f: f}, nil
}

func (l dirFlockLock) lock() error   { return syscall.Flock(int(l.f.Fd()), syscall.LOCK_EX) }
func (l dirFlockLock) unlock() error { return syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN) }
func (l dirFlockLock) close() error  { return l.f.Close() }
