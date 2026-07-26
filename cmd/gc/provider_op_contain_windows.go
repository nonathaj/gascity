//go:build windows

package main

import (
	"os/exec"

	"github.com/gastownhall/gascity/internal/winjob"
)

// containProviderOpProcess places a started provider-op process in an anonymous
// kill-on-close Job Object and returns a function that closes it.
//
// Windows has no process groups, and taskkill /T is not sufficient here: provider
// scripts background their work (`cmd &` in sh), and the resulting grandchild is
// not reliably reachable from the direct child's tree by the time cancellation
// runs. A Job Object is the platform's actual containment primitive — closing the
// last handle terminates every member, whatever the parent links look like.
//
// LIMITATION, stated because it is real: os/exec gives no create-suspended hook, so
// the assignment happens just after Start. Children the child spawned in that
// window are NOT pulled in (see winjob.Assign's doc). Assignment is therefore
// best-effort and cmd.Cancel keeps its taskkill fallback.
func containProviderOpProcess(cmd *exec.Cmd) func() {
	if cmd == nil || cmd.Process == nil {
		return func() {}
	}
	job, err := winjob.Create("", winjob.Limits{KillOnClose: true})
	if err != nil {
		return func() {}
	}
	if err := job.Assign(cmd.Process.Pid); err != nil {
		_ = job.Close()
		return func() {}
	}
	return func() { _ = job.Close() }
}
