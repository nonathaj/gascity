//go:build !windows

package tmux

import (
	"fmt"
	"os/exec"
	"strings"
)

// procChildPIDs returns the direct child PIDs of pid.
func procChildPIDs(pid string) []string {
	out, err := exec.Command("pgrep", "-P", pid).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(strings.TrimSpace(string(out)))
}

// procComm returns the executable name (ps COMM) for pid.
func procComm(pid string) (string, error) {
	out, err := exec.Command("ps", "-p", pid, "-o", "comm=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// procArgs returns the full command line (ps ARGS) for pid.
func procArgs(pid string) (string, error) {
	out, err := exec.Command("ps", "-p", pid, "-o", "args=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// procKillPID signals pid: TERM when force is false, KILL when true.
func procKillPID(pid string, force bool) {
	sig := "-TERM"
	if force {
		sig = "-KILL"
	}
	_ = exec.Command("kill", sig, pid).Run()
}

// procSnapshotAll is only used on Windows; the Unix state cache shells `ps`
// directly (see fetchProcessSnapshot).
func procSnapshotAll() ([]processRuntimeState, error) {
	return nil, fmt.Errorf("procSnapshotAll is windows-only; use the ps snapshot path")
}
