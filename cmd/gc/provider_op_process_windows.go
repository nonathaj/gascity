//go:build windows

package main

import (
	"errors"
	"os"
	"os/exec"

	"github.com/gastownhall/gascity/internal/pidutil"
)

// prepareProviderOpCommand installs the Windows cancellation behavior for a
// provider op, mirroring what the Unix build gets from Setpgid + kill(-pgid).
//
// This used to kill only the direct child, with the note "Windows provider
// cleanup remains direct-process-only until provider ops use job objects". That
// leaked processes: provider scripts run under sh.exe (execshim), so the actual
// work — dolt, bd — is a GRANDCHILD, and terminating sh.exe leaves it running
// with the pipes still open. A cancelled or timed-out provider op therefore
// stranded a dolt server on the machine every time, which is the same class of
// leak as incident gw-qhs (1583 orphaned processes, 93GB).
//
// pidutil.KillTree walks the tree with taskkill /T /F. cmd.Process.Kill stays as
// the backstop for the direct child in case taskkill is unavailable.
func prepareProviderOpCommand(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd == nil || cmd.Process == nil {
			return nil
		}
		if err := pidutil.KillTree(cmd.Process.Pid); err != nil {
			return err
		}
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			return err
		}
		return nil
	}
}
