//go:build windows

package runtime

import (
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// ManagedProcessStopGrace is the shared grace period before escalating
// provider-managed process termination from graceful to forced kill.
const ManagedProcessStopGrace = 5 * time.Second

// SignalProcessGroup approximates Unix group signaling on Windows. There is no
// signal delivery to arbitrary processes; SIGKILL (and any escalation) maps to
// a forced taskkill of the process tree, everything else attempts the graceful
// tree termination taskkill performs without /F (WM_CLOSE / console events).
func SignalProcessGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	if sig == syscall.SIGKILL {
		return taskkillTree(cmd.Process.Pid, true)
	}
	if err := taskkillTree(cmd.Process.Pid, false); err == nil {
		return nil
	}
	return cmd.Process.Signal(sig)
}

// TerminateManagedProcess asks the process tree to exit, waits for done, then
// escalates to a forced tree kill after grace.
func TerminateManagedProcess(cmd *exec.Cmd, done <-chan struct{}, grace time.Duration) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = taskkillTree(cmd.Process.Pid, false)
	timer := time.NewTimer(grace)
	defer timer.Stop()

	select {
	case <-done:
		return nil
	case <-timer.C:
	}

	_ = taskkillTree(cmd.Process.Pid, true)
	<-done
	return nil
}

// taskkillTree terminates pid and its descendants. force adds /F.
func taskkillTree(pid int, force bool) error {
	args := []string{"/T", "/PID", strconv.Itoa(pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	kill := exec.Command("taskkill", args...)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return kill.Run()
}
