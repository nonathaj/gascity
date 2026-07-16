//go:build windows

package main

import "syscall"

// hookChildSysProcAttr gives shell-hook children (on_boot / on_death pipelines)
// a hidden console instead of the default behavior where every console child
// (sh, jq, xargs, and the bd processes xargs fans out) allocates a fresh
// window-station/desktop object. Under a burst of such spawns Windows exhausts
// the desktop heap and children fail to initialize with STATUS_DLL_INIT_FAILED
// (0xc0000142). CREATE_NO_WINDOW gives the child a hidden console its own
// descendants inherit, keeping the whole pipeline off the interactive desktop.
func hookChildSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: winCreateNoWindow,
		HideWindow:    true,
	}
}
