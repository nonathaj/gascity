//go:build !windows

package beads

import (
	"fmt"
	"os"
	"syscall"
)

// sequenceFloorDirLockIsFree reports whether the sequence-floor lock on dir
// could be taken right now, without blocking. A test uses it to prove the lock
// is genuinely held while the guarded section runs.
//
// This mirrors lockSQLiteSequenceFloorDir's Unix mechanism: a non-blocking
// flock on the directory, which is what a competing process would attempt.
func sequenceFloorDirLockIsFree(dir string) (bool, error) {
	contender, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer contender.Close() //nolint:errcheck // probe descriptor
	if err := syscall.Flock(int(contender.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			return false, nil
		}
		return false, fmt.Errorf("probing sequence-floor lock: %w", err)
	}
	_ = syscall.Flock(int(contender.Fd()), syscall.LOCK_UN)
	return true, nil
}
