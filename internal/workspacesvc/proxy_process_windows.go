//go:build windows

package workspacesvc

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
)

func stopProcessGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = taskkillProxyTree(cmd.Process.Pid, false)
	deadline := time.Now().Add(proxyProcessShutdownWait)
	for time.Now().Before(deadline) {
		if !pidutil.Alive(cmd.Process.Pid) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = taskkillProxyTree(cmd.Process.Pid, true)
	return nil
}

// taskkillProxyTree terminates pid and its descendants. force adds /F.
func taskkillProxyTree(pid int, force bool) error {
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return kill.Run()
}

// proxyProcessSysProcAttr returns the process attributes used to spawn a
// proxy_process child. Windows has neither Pdeathsig nor Setpgid; the
// equivalent of a new process group is CREATE_NEW_PROCESS_GROUP, which is
// also what processgroup.StartCommandInNewGroup sets, so a Ctrl-Break aimed
// at the proxy's group cannot reach the parent. Teardown is taskkill /T
// (stopProcessGroup above), which walks parent links rather than the group.
func proxyProcessSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
