//go:build !windows

package main

import "syscall"

// platformGetpgid resolves the process group of pid.
func platformGetpgid(pid int) (int, error) { return syscall.Getpgid(pid) }

// platformGetpgrp returns the current process group.
func platformGetpgrp() int { return syscall.Getpgrp() }

// platformKill delivers sig to pid (signal 0 probes liveness).
func platformKill(pid int, sig syscall.Signal) error { return syscall.Kill(pid, sig) }

// platformCloseOnExec marks fd as not inherited by child processes.
func platformCloseOnExec(fd int) { syscall.CloseOnExec(fd) }

// platformFreeDiskBytes reports the free bytes available on path's filesystem.
func platformFreeDiskBytes(path string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

// platformKillGroup delivers sig to the process group led by pid.
func platformKillGroup(pid int, sig syscall.Signal) error { return syscall.Kill(-pid, sig) }
