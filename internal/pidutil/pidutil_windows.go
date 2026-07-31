//go:build windows

package pidutil

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

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

// Cmdline returns a PID's command line, normalized through NormalizeArgv.
// Windows has no /proc; the command line is read out of the target's PEB
// (NtQueryInformationProcess → PEB → ProcessParameters → CommandLine, the
// same walk the proctable env reader and dolt discovery use) and split
// into argv with the platform's own CommandLineToArgvW rules. Only
// same-user, same-bitness processes are readable; an unreadable target
// returns an error, which AliveWithCmdline treats as fail-closed —
// exactly like an unreadable /proc record on Linux (gw-opy: the old
// fallback ignored the matcher and reported any live PID as a match).
func Cmdline(pid int) ([]string, error) {
	raw, err := windowsProcessCommandLine(pid)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	argv, err := windows.DecomposeCommandLine(raw)
	if err != nil {
		return nil, fmt.Errorf("pidutil: decomposing command line of PID %d: %w", pid, err)
	}
	return NormalizeArgv(argv), nil
}

// Supported reports whether Cwd can work on this platform.
func Supported() bool { return true }

// Cwd returns a PID's current working directory.
//
// Windows has no /proc/<pid>/cwd and no lsof, so the value comes from the
// target's PEB (ProcessParameters → CurrentDirectory.DosPath) — the same walk
// Cmdline uses. Only same-user, same-bitness processes are readable; anything
// else returns an error, which callers must treat as "no ownership evidence"
// rather than as a mismatch.
//
// The stored DosPath keeps a trailing separator ("C:\dir\"); it is trimmed here
// so the result compares equal to a filepath.Clean'd path.
func Cwd(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pidutil: invalid pid %d", pid)
	}
	raw, err := windowsProcessParameterString(pid, func(p *windows.RTL_USER_PROCESS_PARAMETERS) *windows.NTUnicodeString {
		return &p.CurrentDirectory.DosPath
	})
	if err != nil {
		return "", err
	}
	if raw == "" {
		return "", fmt.Errorf("pidutil: PID %d reported no working directory", pid)
	}
	return strings.TrimRight(raw, `\/`), nil
}

// windowsProcessCommandLine reads another process's command line by
// walking its PEB.
func windowsProcessCommandLine(pid int) (string, error) {
	return windowsProcessParameterString(pid, func(p *windows.RTL_USER_PROCESS_PARAMETERS) *windows.NTUnicodeString {
		return &p.CommandLine
	})
}

// windowsProcessParameterString walks a PID's PEB to its
// RTL_USER_PROCESS_PARAMETERS and reads one UNICODE_STRING member out of the
// target's address space. Both the command line and the working directory live
// there, so the walk is shared rather than duplicated per field.
func windowsProcessParameterString(
	pid int,
	pick func(*windows.RTL_USER_PROCESS_PARAMETERS) *windows.NTUnicodeString,
) (string, error) {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return "", fmt.Errorf("pidutil: opening PID %d: %w", pid, err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // read-only handle

	var pbi windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil); err != nil {
		return "", fmt.Errorf("pidutil: querying PID %d: %w", pid, err)
	}
	if pbi.PebBaseAddress == nil {
		return "", fmt.Errorf("pidutil: PID %d has no readable PEB", pid)
	}

	var peb windows.PEB
	if err := readProcessMemory(h, uintptr(unsafe.Pointer(pbi.PebBaseAddress)),
		unsafe.Pointer(&peb), unsafe.Sizeof(peb)); err != nil {
		return "", fmt.Errorf("pidutil: reading PEB of PID %d: %w", pid, err)
	}
	if peb.ProcessParameters == nil {
		return "", fmt.Errorf("pidutil: PID %d has no process parameters", pid)
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	if err := readProcessMemory(h, uintptr(unsafe.Pointer(peb.ProcessParameters)),
		unsafe.Pointer(&params), unsafe.Sizeof(params)); err != nil {
		return "", fmt.Errorf("pidutil: reading process parameters of PID %d: %w", pid, err)
	}

	field := pick(&params)
	n := uintptr(field.Length)
	if n == 0 || field.Buffer == nil {
		return "", nil
	}
	buf := make([]uint16, n/2)
	if err := readProcessMemory(h, uintptr(unsafe.Pointer(field.Buffer)),
		unsafe.Pointer(&buf[0]), n); err != nil {
		return "", fmt.Errorf("pidutil: reading process parameter of PID %d: %w", pid, err)
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

// StartTime is unsupported on Windows (no /proc start-time equivalent). Callers
// treat an error as "no identity signal available" — AliveWithStartTime falls
// back to plain liveness (see its doc), which is the same behavior taken on
// darwin and any other host without /proc.
func StartTime(int) (string, error) {
	return "", errors.New("pidutil: start-time inspection is not supported on windows")
}
