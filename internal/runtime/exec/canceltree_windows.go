//go:build windows

package exec

import (
	"os/exec"
	"strconv"
)

// cancelKillTree returns a cmd.Cancel that terminates the whole process
// tree on context cancellation. execshim wraps a .sh provider in sh.exe,
// and exec's default cancel (TerminateProcess on sh.exe only) orphans
// the script grandchild that still holds the stdout pipe open, so the
// reader never sees EOF (gw-ho3). taskkill /T /F reaps the tree.
// Fire-and-forget: it does not wait for confirmed death (that is
// WaitDelay's job), so Start/Run returns promptly.
func cancelKillTree(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		// /T kills the tree, /F forces; errors (already-gone) are
		// non-fatal — the caller's WaitDelay still closes the pipes.
		_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
		return cmd.Process.Kill()
	}
}
