//go:build !windows

package subprocess

// sessionJob is Windows-only containment (see sessionjob_windows.go); on
// Unix the process group started by StartCommandInNewGroup already owns the
// whole tree and SIGKILL to the group is the teardown mechanism.
type sessionJob struct{}

func newSessionJob(int) *sessionJob { return nil }

func (s *sessionJob) terminateTree() {}
