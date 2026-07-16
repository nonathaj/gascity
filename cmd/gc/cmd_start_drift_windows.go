//go:build windows

package main

import "golang.org/x/sys/windows"

// readSupervisorExePath returns the on-disk path of the supervisor's
// executable. Windows has no /proc/<pid>/exe; QueryFullProcessImageName is the
// supported way to resolve a running process's image path, and it needs only
// PROCESS_QUERY_LIMITED_INFORMATION (granted for a same-user process like the
// supervisor). Unlike Linux there is no " (deleted)" decoration: a running
// image file cannot be unlinked/overwritten in place on Windows, so drift
// replaces the binary at the same path and the returned path is exactly what
// auto-restart needs to re-spawn.
func readSupervisorExePath(pid int) (string, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort close of a query handle

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf[:size]), nil
}
