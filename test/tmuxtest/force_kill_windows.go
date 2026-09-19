//go:build windows

package tmuxtest

import "github.com/gastownhall/gascity/internal/pidutil"

// forceKillProcess terminates pid immediately. Windows has no SIGKILL; the
// equivalent is a forced TerminateProcess, and because the multiplexer
// (psmux) runs its panes as descendants, the whole tree goes with it —
// which is exactly what a wedged tmux server's teardown needs.
func forceKillProcess(pid int) error {
	return pidutil.KillTree(pid)
}
