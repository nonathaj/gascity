//go:build windows

package processgroup

import (
	"os/exec"

	"github.com/gastownhall/gascity/internal/winjob"
)

// Containment holds a durable grouping for a started command's descendants.
//
// Windows has no process groups in the POSIX sense. taskkill /T walks LIVE parent links, so
// it only reaches descendants whose chain of parents is still intact. That is exactly what
// breaks here: a shell command ending in `&` backgrounds its work and the outer shell exits
// immediately, orphaning the real worker. By cancellation time the leader is gone and the
// worker is not reachable as anyone's descendant, so it runs forever. On Unix the same code
// works because a process group outlives its leader and kill(-pgid) still reaches the orphan.
//
// A Job Object is the platform's actual answer: membership is permanent and survives any
// parent exiting, so terminating the job reaches every descendant regardless of parent links.
type Containment struct {
	job *winjob.Job
}

// Contain places an already-started command in a Job Object.
//
// The job is created WITHOUT kill-on-close, which is the important detail. Release must be
// safe to call on the success path, and some commands background a daemon on purpose — a
// pack's start op launching a server through order exec is the obvious case. A kill-on-close
// job would terminate that server the moment the runner returned successfully. Killing is
// therefore explicit, via Terminate, on cancellation paths only.
//
// LIMITATION, stated because it is real: os/exec offers no create-suspended hook, so
// assignment happens just after Start. A descendant spawned in that window is not pulled in.
// Containment is an addition to the existing taskkill path, not a replacement for it.
func Contain(cmd *exec.Cmd) *Containment {
	if cmd == nil || cmd.Process == nil {
		return &Containment{}
	}
	job, err := winjob.Create("", winjob.Limits{})
	if err != nil {
		return &Containment{}
	}
	if err := job.Assign(cmd.Process.Pid); err != nil {
		_ = job.Close()
		return &Containment{}
	}
	return &Containment{job: job}
}

// Terminate kills every process in the job, including orphaned descendants.
func (c *Containment) Terminate() error {
	if c == nil || c.job == nil {
		return nil
	}
	return c.job.Terminate(1)
}

// Release drops the job handle without killing its members.
func (c *Containment) Release() {
	if c == nil || c.job == nil {
		return
	}
	_ = c.job.Close()
	c.job = nil
}
