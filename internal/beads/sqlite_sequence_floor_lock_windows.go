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

// sequenceFloorLock is the cross-process lock behind
// persistSQLiteSequenceFloorAtLeast. lock blocks until held; unlock releases
// it; close discards the handle. Each platform file supplies the one shape
// that satisfies the directory-lock rationale documented on that function.
type sequenceFloorLock interface {
	lock() error
	unlock() error
	close() error
}

// namedMutexLock is a kernel mutex named after the store directory. Windows
// refuses LockFileEx on a directory handle, and a sibling lock file is ruled
// out by the censused-namespace contract, so the lock lives in the kernel
// object namespace instead: no filesystem entry, shared by every process that
// derives the same name, and abandoned (hence released) when a holder dies.
type namedMutexLock struct{ h windows.Handle }

// openSequenceFloorLock creates or opens the mutex for dir. The name is a
// digest of the cleaned, case-folded absolute path so every spelling of the
// same directory converges on one object, and it stays under the Local\
// session namespace so an unrelated user session on the host cannot collide.
func openSequenceFloorLock(dir string) (sequenceFloorLock, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(abs))))
	name, err := windows.UTF16PtrFromString(`Local\gascity-seqfloor-` + hex.EncodeToString(sum[:16]))
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		return nil, fmt.Errorf("creating sequence-floor mutex: %w", err)
	}
	return namedMutexLock{h: h}, nil
}

func (l namedMutexLock) lock() error {
	event, err := windows.WaitForSingleObject(l.h, windows.INFINITE)
	if err != nil {
		return err
	}
	switch event {
	case windows.WAIT_OBJECT_0, windows.WAIT_ABANDONED:
		// WAIT_ABANDONED means a previous holder died without releasing; the
		// mutex is ours now, and the floor file it guarded is re-read before
		// being rewritten, so the state is safe to reconcile.
		return nil
	default:
		return fmt.Errorf("waiting for sequence-floor mutex: unexpected wait result %#x", event)
	}
}

func (l namedMutexLock) unlock() error { return windows.ReleaseMutex(l.h) }
func (l namedMutexLock) close() error  { return windows.CloseHandle(l.h) }
