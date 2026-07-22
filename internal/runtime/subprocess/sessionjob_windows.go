//go:build windows

package subprocess

import (
	"github.com/gastownhall/gascity/internal/winjob"
)

// sessionJob holds the Job Object containing a session's process tree.
//
// Created WITHOUT KillOnClose: a session must survive this process exiting
// (when the handle closes the job simply dissolves and members live on).
// Stop uses Terminate, which kills every member — including grandchildren
// MSYS fork emulation reparents to a dead helper PID, which no parent-tree
// walk (taskkill /T, descendant snapshots) can ever reach (gw-say).
//
// Children spawned after Assign inherit membership automatically; the only
// gap is a grandchild forked in the few milliseconds between process start
// and Assign, which the taskkill pass in TerminateManagedProcess still
// covers for live parent links.
type sessionJob struct {
	job *winjob.Job
}

// newSessionJob creates an anonymous job and assigns pid. Best-effort: on
// any failure it returns nil and teardown falls back to the taskkill path.
func newSessionJob(pid int) *sessionJob {
	j, err := winjob.Create("", winjob.Limits{})
	if err != nil {
		return nil
	}
	if err := j.Assign(pid); err != nil {
		_ = j.Close()
		return nil
	}
	return &sessionJob{job: j}
}

// terminateTree force-kills every job member and releases the handle.
// Safe on nil and after a prior call.
func (s *sessionJob) terminateTree() {
	if s == nil || s.job == nil {
		return
	}
	_ = s.job.Terminate(1)
	_ = s.job.Close()
	s.job = nil
}
