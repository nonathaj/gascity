//go:build windows

package events

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Windows byte-range locks are MANDATORY, not advisory: locking byte 0 of the
// live log would block every concurrent reader (ReadLatestSeq, tailers). To
// get flock-like advisory semantics the recorder locks one byte at a huge
// offset far past any real data, so the lock never overlaps actual reads or
// writes and only ever contends with other recorders taking the same range.
const (
	recorderLockOffsetLow  = 0xFFFFFFFE
	recorderLockOffsetHigh = 0x7FFFFFFF
)

func recorderLockOverlapped() *windows.Overlapped {
	return &windows.Overlapped{Offset: recorderLockOffsetLow, OffsetHigh: recorderLockOffsetHigh}
}

// tryLockRecorderFile attempts a non-blocking exclusive cross-process lock on f.
func tryLockRecorderFile(f *os.File) error {
	return windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, recorderLockOverlapped())
}

// recorderLockWouldBlock reports whether err means another writer holds the lock.
func recorderLockWouldBlock(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// unlockRecorderFile releases the cross-process lock on f.
func unlockRecorderFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, recorderLockOverlapped())
}
