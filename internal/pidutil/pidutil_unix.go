//go:build !windows

package pidutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const psZombieTimeout = 100 * time.Millisecond

// Alive reports whether a PID exists and is not a zombie.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	statPath := filepath.Join("/proc", strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(statPath)
	if err != nil {
		return !psReportsZombie(pid)
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 && fields[2] == "Z" {
		return false
	}
	return true
}

// StartTime returns a PID's start time — field 22 (starttime, in clock ticks
// since boot) of /proc/<pid>/stat — as an opaque token used to disambiguate a
// recycled PID from the original target. The kernel never reuses a (pid,
// starttime) pair for the lifetime of a boot, so a changed start time on the
// same PID proves the original process is gone and an unrelated one now holds
// the number. It returns an error on platforms without /proc (e.g. darwin) or
// when the process record is unreadable; callers treat that as "no identity
// signal available" and fall back to plain liveness.
//
// The comm field (field 2) is wrapped in parens and may itself contain spaces
// and parens, so parsing anchors on the final ')' and counts fields from
// there: field 3 (state) is the first token after "') '", making field 22
// (starttime) the token at index 19 of that suffix.
func StartTime(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pidutil: invalid PID %d", pid)
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	stat := string(data)
	rparen := strings.LastIndexByte(stat, ')')
	if rparen < 0 || rparen+2 >= len(stat) {
		return "", fmt.Errorf("pidutil: malformed stat for PID %d", pid)
	}
	fields := strings.Fields(stat[rparen+2:])
	const starttimeIndexAfterComm = 19 // field 22 minus fields 1-3 offset
	if len(fields) <= starttimeIndexAfterComm {
		return "", fmt.Errorf("pidutil: stat for PID %d has %d post-comm fields, want > %d", pid, len(fields), starttimeIndexAfterComm)
	}
	return fields[starttimeIndexAfterComm], nil
}

// Cmdline returns a PID's command line from /proc, normalized through
// NormalizeArgv. Hosts without /proc cmdline support (darwin) return
// ErrCmdlineUnsupported so AliveWithCmdline can fall back to Alive; an
// unreadable record on a /proc host is a real error and fails closed.
func Cmdline(pid int) ([]string, error) {
	if runtime.GOOS != "linux" {
		return nil, ErrCmdlineUnsupported
	}
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(data), "\x00")
	if trimmed == "" {
		return nil, nil
	}
	return NormalizeArgv(strings.Split(trimmed, "\x00")), nil
}

func psReportsZombie(pid int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), psZombieTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(out))
	return strings.HasPrefix(state, "Z")
}
