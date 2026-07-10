//go:build !windows

package main

import (
	"os"
	"syscall"
)

// backgroundSysProcAttr returns SysProcAttr for detaching a background child
// from the parent's process group, so it survives parent exit.
func backgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// supervisorEnv is the operator environment as-is on Unix.
func supervisorEnv() []string {
	return os.Environ()
}

// detachedSupervisorAttrs has a single candidate on Unix.
func detachedSupervisorAttrs() []*syscall.SysProcAttr {
	return []*syscall.SysProcAttr{backgroundSysProcAttr()}
}
