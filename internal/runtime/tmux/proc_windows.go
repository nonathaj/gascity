//go:build windows

package tmux

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procChildPIDs returns the direct child PIDs of pid via a toolhelp
// process snapshot (the native equivalent of `pgrep -P`).
func procChildPIDs(pid string) []string {
	parent, err := strconv.ParseUint(pid, 10, 32)
	if err != nil {
		return nil
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck // best-effort handle close

	var out []string
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil
	}
	for {
		if uint64(entry.ParentProcessID) == parent {
			out = append(out, strconv.FormatUint(uint64(entry.ProcessID), 10))
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return out
}

// procComm returns the executable name for pid from the process snapshot.
func procComm(pid string) (string, error) {
	target, err := strconv.ParseUint(pid, 10, 32)
	if err != nil {
		return "", err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck // best-effort handle close

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return "", err
	}
	for {
		if uint64(entry.ProcessID) == target {
			name := windows.UTF16ToString(entry.ExeFile[:])
			return strings.TrimSuffix(name, ".exe"), nil
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return "", fmt.Errorf("pid %s not found", pid)
}

// procArgs approximates the Unix full command line. Reading another process's
// argv on Windows requires PEB access; the executable name is enough for the
// name-matching callers in this package.
func procArgs(pid string) (string, error) {
	return procComm(pid)
}

// procKillPID terminates pid: taskkill graceful when force is false, /F when
// true.
func procKillPID(pid string, force bool) {
	args := []string{"/PID", pid}
	if force {
		args = append([]string{"/F"}, args...)
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = kill.Run()
}

// procSnapshotAll enumerates the full process table (PID, PPID, executable
// name) via a toolhelp snapshot — the Windows equivalent of the `ps` sweep
// the state cache performs. Args mirrors Command; reading another process's
// argv would require PEB access and the state cache's name matching only
// needs the executable identity.
func procSnapshotAll() ([]processRuntimeState, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot) //nolint:errcheck // best-effort handle close

	var out []processRuntimeState
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	for {
		name := strings.TrimSuffix(windows.UTF16ToString(entry.ExeFile[:]), ".exe")
		out = append(out, processRuntimeState{
			PID:     strconv.FormatUint(uint64(entry.ProcessID), 10),
			PPID:    strconv.FormatUint(uint64(entry.ParentProcessID), 10),
			Command: name,
			Args:    name,
		})
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return out, nil
}
