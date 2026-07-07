//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/gastownhall/gascity/internal/pidutil"
)

// platformGetpgid resolves the process group of pid. Windows has no process
// groups in the Unix sense; the tree rooted at pid stands in for the group.
func platformGetpgid(pid int) (int, error) {
	if !pidutil.Alive(pid) {
		return 0, syscall.ESRCH
	}
	return pid, nil
}

// platformGetpgrp returns the current process group (the process itself).
func platformGetpgrp() int { return os.Getpid() }

// platformKill approximates Unix kill(2). Signal 0 probes liveness; SIGKILL
// forces a tree kill; anything else attempts taskkill's graceful termination.
// Returns ESRCH when the process is already gone, matching Unix semantics.
func platformKill(pid int, sig syscall.Signal) error {
	if !pidutil.Alive(pid) {
		return syscall.ESRCH
	}
	if sig == 0 {
		return nil
	}
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if sig == syscall.SIGKILL {
		args = append([]string{"/F"}, args...)
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return kill.Run()
}

// platformCloseOnExec marks fd (a Windows handle value) as not inherited by
// child processes.
func platformCloseOnExec(fd int) {
	_ = windows.SetHandleInformation(windows.Handle(fd), windows.HANDLE_FLAG_INHERIT, 0)
}

// platformFreeDiskBytes reports the free bytes available on path's volume.
func platformFreeDiskBytes(path string) (uint64, error) {
	var free, total, totalFree uint64
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}

// platformKillGroup terminates the process tree rooted at pid — the Windows
// analogue of signaling a Unix process group.
func platformKillGroup(pid int, sig syscall.Signal) error { return platformKill(pid, sig) }
