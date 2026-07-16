//go:build windows

package main

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// discoverDoltProcessesPlatform enumerates managed dolt sql-server processes on
// Windows, which has neither /proc nor `ps`. It snapshots the process table via
// Toolhelp32 to find dolt.exe images, then reads each candidate's command line
// out of its PEB (the same PEB-walk the agent process table uses for env) and
// applies the shared looksLikeDoltSQLServer predicate — so managed-dolt cleanup
// and the test leak guard get parity with the Unix paths. Only same-user,
// 64-bit processes are readable; others are skipped (dolt.exe is 64-bit).
//
// Ports come from portsByPID, which is /proc/lsof-based and yields nothing on
// Windows, so the reaper falls back to its --config path filter (which this
// path still populates). Windows TCP-table port attribution is separate
// follow-up work.
func discoverDoltProcessesPlatform() ([]DoltProcInfo, bool, error) {
	pids, err := doltImagePIDs()
	if err != nil {
		return nil, true, err
	}
	pidPorts := portsByPID()
	var out []DoltProcInfo
	for _, pid := range pids {
		cmdline, err := readWindowsProcessCommandLine(pid)
		if err != nil || cmdline == "" {
			continue
		}
		argv, err := windows.DecomposeCommandLine(cmdline)
		if err != nil || !looksLikeDoltSQLServer(argv) {
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

// readWindowsProcessCommandLine reads another process's command line by walking
// its PEB (NtQueryInformationProcess → PEB → ProcessParameters → CommandLine),
// mirroring the environment read the agent process table performs.
func readWindowsProcessCommandLine(pid int) (string, error) {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h) //nolint:errcheck // read-only handle

	var pbi windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil); err != nil {
		return "", err
	}
	if pbi.PebBaseAddress == nil {
		return "", fmt.Errorf("process %d has no readable PEB", pid)
	}

	var peb windows.PEB
	if err := readProcessMemory(h, uintptr(unsafe.Pointer(pbi.PebBaseAddress)),
		unsafe.Pointer(&peb), unsafe.Sizeof(peb)); err != nil {
		return "", err
	}
	if peb.ProcessParameters == nil {
		return "", fmt.Errorf("process %d has no process parameters", pid)
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	if err := readProcessMemory(h, uintptr(unsafe.Pointer(peb.ProcessParameters)),
		unsafe.Pointer(&params), unsafe.Sizeof(params)); err != nil {
		return "", err
	}
	n := uintptr(params.CommandLine.Length)
	if n == 0 || params.CommandLine.Buffer == nil {
		return "", nil
	}
	buf := make([]uint16, n/2)
	if err := readProcessMemory(h, uintptr(unsafe.Pointer(params.CommandLine.Buffer)),
		unsafe.Pointer(&buf[0]), n); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}

func readProcessMemory(h windows.Handle, addr uintptr, dst unsafe.Pointer, size uintptr) error {
	var read uintptr
	if err := windows.ReadProcessMemory(h, addr, (*byte)(dst), size, &read); err != nil {
		return err
	}
	if read != size {
		return fmt.Errorf("short read: %d of %d bytes", read, size)
	}
	return nil
}
