//go:build !windows

package tmuxtest

import "syscall"

// forceKillProcess terminates pid immediately. SIGKILL cannot be caught or
// ignored, which is the point: this is the reaper's last resort for a tmux
// server that survived kill-server.
func forceKillProcess(pid int) error {
	return syscall.Kill(pid, syscall.SIGKILL)
}
