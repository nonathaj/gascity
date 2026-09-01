//go:build !linux

package workspacesvc

import "os/exec"

// applyProxyProcessPdeathsig is a no-op off Linux: no other platform offers a
// kernel-enforced parent-death signal. The child is still started in its own
// process group by processgroup.StartCommandInNewGroup, which is the portable
// half of the guarantee.
//
// This deliberately does not build a syscall.SysProcAttr: its fields are
// platform-specific (Setpgid and Pdeathsig do not exist on Windows), so naming
// them here would break the Windows build.
func applyProxyProcessPdeathsig(*exec.Cmd) {}
