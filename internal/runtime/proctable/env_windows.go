//go:build windows

package proctable

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// maxEnvBlockBytes caps how much of a foreign process's environment block is
// read. Real blocks are a few KB; the cap guards against a corrupt
// EnvironmentSize sending us on a multi-megabyte read.
const maxEnvBlockBytes = 1 << 20

// readProcessEnv reads another process's environment variables by walking
// its PEB: NtQueryInformationProcess gives the PEB address, and
// ReadProcessMemory pulls PEB → ProcessParameters → Environment (a UTF-16
// double-NUL-terminated block, sized by EnvironmentSize). Only same-user
// processes are readable (OpenProcess denies the rest), which matches the
// scan's purpose: finding this user's escaped agent runtimes.
//
// Reading a 32-bit (WOW64) process from a 64-bit scanner would need the
// 32-bit PEB layout; agent runtimes are 64-bit, so those reads just fail
// and the process is skipped.
func readProcessEnv(pid int) (map[string]string, error) {
	h, err := windows.OpenProcess(
		windows.PROCESS_QUERY_INFORMATION|windows.PROCESS_VM_READ, false, uint32(pid))
	if err != nil {
		return nil, fmt.Errorf("open process %d: %w", pid, err)
	}
	defer windows.CloseHandle(h) //nolint:errcheck // read-only handle

	var pbi windows.PROCESS_BASIC_INFORMATION
	if err := windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation,
		unsafe.Pointer(&pbi), uint32(unsafe.Sizeof(pbi)), nil); err != nil {
		return nil, fmt.Errorf("query process %d: %w", pid, err)
	}
	if pbi.PebBaseAddress == nil {
		return nil, fmt.Errorf("process %d has no readable PEB", pid)
	}

	var peb windows.PEB
	if err := readMem(h, uintptr(unsafe.Pointer(pbi.PebBaseAddress)),
		unsafe.Pointer(&peb), unsafe.Sizeof(peb)); err != nil {
		return nil, fmt.Errorf("read PEB of %d: %w", pid, err)
	}
	if peb.ProcessParameters == nil {
		return nil, fmt.Errorf("process %d has no process parameters", pid)
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	if err := readMem(h, uintptr(unsafe.Pointer(peb.ProcessParameters)),
		unsafe.Pointer(&params), unsafe.Sizeof(params)); err != nil {
		return nil, fmt.Errorf("read process parameters of %d: %w", pid, err)
	}
	if params.Environment == nil || params.EnvironmentSize == 0 {
		return map[string]string{}, nil
	}
	size := params.EnvironmentSize
	if size > maxEnvBlockBytes {
		size = maxEnvBlockBytes
	}

	buf := make([]byte, size)
	if err := readMem(h, uintptr(params.Environment),
		unsafe.Pointer(&buf[0]), uintptr(len(buf))); err != nil {
		return nil, fmt.Errorf("read environment block of %d: %w", pid, err)
	}
	return parseUTF16EnvBlock(buf), nil
}

func readMem(h windows.Handle, addr uintptr, dst unsafe.Pointer, size uintptr) error {
	var read uintptr
	if err := windows.ReadProcessMemory(h, addr, (*byte)(dst), size, &read); err != nil {
		return err
	}
	if read != size {
		return fmt.Errorf("short read: %d of %d bytes", read, size)
	}
	return nil
}

// parseUTF16EnvBlock decodes a Windows environment block: UTF-16LE
// KEY=VALUE strings, each NUL-terminated, with an empty string terminating
// the block. Entries without '=' or with an empty key (the drive-letter
// "=C:=..." bookkeeping entries) are skipped.
func parseUTF16EnvBlock(block []byte) map[string]string {
	env := make(map[string]string)
	u16 := make([]uint16, len(block)/2)
	for i := range u16 {
		u16[i] = uint16(block[2*i]) | uint16(block[2*i+1])<<8
	}
	start := 0
	for i := 0; i < len(u16); i++ {
		if u16[i] != 0 {
			continue
		}
		if i == start { // empty string: end of block
			break
		}
		entry := string(utf16.Decode(u16[start:i]))
		start = i + 1
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	return env
}
