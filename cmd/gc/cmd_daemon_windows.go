//go:build windows

package main

import "syscall"

// daemonSysProcAttr returns nil on Windows (no process group detachment).
func daemonSysProcAttr() *syscall.SysProcAttr {
	return nil
}
