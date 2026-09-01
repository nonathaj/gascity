//go:build !windows

package beads

import (
	"fmt"
	"os"
	"syscall"
)

// lockSQLiteSequenceFloorDir takes the exclusive cross-process lock on dir and
// returns the release. See persistSQLiteSequenceFloorAtLeast for why the lock
// target is the directory rather than the floor file or the database.
func lockSQLiteSequenceFloorDir(dir string) (func() error, error) {
	lock, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("opening sequence-floor lock directory: %w", err)
	}
	observeSQLiteSequenceFloorBoundary("sequence-floor-lock-open")
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("locking SQLite sequence floor: %w", err)
	}
	return func() error {
		var unlockErr error
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
			unlockErr = fmt.Errorf("unlocking SQLite sequence floor: %w", err)
		}
		observeSQLiteSequenceFloorBoundary("sequence-floor-lock-close-before")
		if err := lock.Close(); err != nil {
			if unlockErr != nil {
				return unlockErr
			}
			return fmt.Errorf("closing SQLite sequence-floor lock descriptor: %w", err)
		}
		observeSQLiteSequenceFloorBoundary("sequence-floor-lock-close-after")
		return unlockErr
	}, nil
}
