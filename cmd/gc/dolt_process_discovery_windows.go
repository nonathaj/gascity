//go:build windows

package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/gastownhall/gascity/internal/pidutil"
)

// discoverDoltProcessesPlatform enumerates managed dolt sql-server processes on
// Windows, which has neither /proc nor `ps`. It snapshots the process table via
// Toolhelp32 to find dolt.exe images, then reads each candidate's command line
// via pidutil.Cmdline (the shared PEB walk) and applies the shared
// looksLikeDoltSQLServer predicate — so managed-dolt cleanup and the test leak
// guard get parity with the Unix paths. Only same-user, 64-bit processes are
// readable; others are skipped (dolt.exe is 64-bit). Ports come from
// portsByPID, which now uses the shared GetExtendedTcpTable attribution on
// Windows (pidutil.TCPListenerPortsByPID).
func discoverDoltProcessesPlatform() ([]DoltProcInfo, bool, error) {
	pids, err := doltImagePIDs()
	if err != nil {
		return nil, true, err
	}
	pidPorts := portsByPID()
	var out []DoltProcInfo
	for _, pid := range pids {
		argv, err := pidutil.Cmdline(pid)
		if err != nil || len(argv) == 0 || !looksLikeDoltSQLServer(argv) {
			continue
		}
		out = append(out, DoltProcInfo{
			PID:             pid,
			Argv:            argv,
			Ports:           pidPorts[pid],
			ConfigPathState: doltConfigPathState(argv),
		})
	}
	return out, true, nil
}

// doltImagePIDs returns the PIDs whose image name is dolt.exe — a cheap
// Toolhelp32 first filter before the more expensive per-process PEB read.
func doltImagePIDs() ([]int, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("dolt discovery: process snapshot: %w", err)
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck // read-only snapshot handle

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("dolt discovery: first process entry: %w", err)
	}
	var pids []int
	for {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "dolt.exe") {
			pids = append(pids, int(entry.ProcessID))
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break // ERROR_NO_MORE_FILES ends the walk
		}
	}
	return pids, nil
}
