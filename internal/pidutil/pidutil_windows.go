//go:build windows

package pidutil

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Alive reports whether a PID exists. Windows has no zombie state: a process
// object either still runs (exit code STILL_ACTIVE) or has exited.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// Access denied means the process exists but belongs to another
		// user/integrity level — mirror the EPERM handling on Unix.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort handle close
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// Cmdline is unsupported on Windows (no /proc cmdline equivalent without PEB
// spelunking). Callers treat an error as "cannot verify" — AliveWithCmdline
// already short-circuits to Alive on non-Linux hosts.
func Cmdline(int) ([]string, error) {
	return nil, errors.New("pidutil: cmdline inspection is not supported on windows")
}
