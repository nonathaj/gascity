//go:build windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// platformFileOpenState reports whether path is currently open by any process,
// the Windows-native replacement for the /proc + lsof probe. It requests an
// exclusive open (share mode 0): if another process holds the file — as a
// running managed-Dolt server holds its data files (verified: those files
// cannot even be deleted while it runs) — CreateFile fails with
// ERROR_SHARING_VIOLATION. A clean exclusive open, or the file being gone, means
// nothing holds it. FILE_FLAG_BACKUP_SEMANTICS lets the same probe open a
// directory handle. Ambiguous failures (access denied) return checked=false so
// the caller treats the state as unknown, matching the Unix fall-through.
func platformFileOpenState(path string) (open bool, checked bool, err error) {
	p, perr := windows.UTF16PtrFromString(path)
	if perr != nil {
		return false, false, nil
	}
	h, cerr := windows.CreateFile(
		p,
		windows.GENERIC_READ,
		0, // exclusive: no sharing
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if cerr == nil {
		_ = windows.CloseHandle(h)
		return false, true, nil
	}
	switch {
	case errors.Is(cerr, windows.ERROR_SHARING_VIOLATION):
		return true, true, nil
	case errors.Is(cerr, windows.ERROR_FILE_NOT_FOUND),
		errors.Is(cerr, windows.ERROR_PATH_NOT_FOUND):
		return false, true, nil
	default:
		// Access-denied and other errors are ambiguous; report unknown so the
		// caller keeps its conservative behavior rather than a wrong "not open".
		return false, false, nil
	}
}
