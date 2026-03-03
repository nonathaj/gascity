//go:build !windows

package main

import "syscall"

// daemonSysProcAttr returns SysProcAttr for detaching the child from the
// parent's process group (Setpgid), so the daemon survives parent exit.
func daemonSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
