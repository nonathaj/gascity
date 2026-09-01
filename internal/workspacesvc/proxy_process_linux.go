//go:build linux

package workspacesvc

import (
	"os/exec"
	"syscall"
)

// applyProxyProcessPdeathsig asks the kernel to SIGKILL a proxy_process child
// when its parent dies. Pdeathsig is kernel-enforced: it fires no matter how
// the supervisor process ends, including the Go test -timeout watchdog's
// direct os.Exit (which runs no defer or t.Cleanup anywhere in the process),
// so it is the only way to guarantee the child does not survive a hard parent
// exit (ga-9br097).
//
// The process group itself is established by
// processgroup.StartCommandInNewGroup, which is portable; this only adds the
// Linux-specific death signal. SysProcAttr is already non-nil by the time this
// runs, but the nil guard keeps the two calls order-independent.
func applyProxyProcessPdeathsig(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
