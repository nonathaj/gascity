//go:build windows

// Package processgroup provides process-group cleanup helpers. On Windows a
// "group" is approximated by the process tree rooted at the group-leader PID:
// StartCommandInNewGroup creates the child in a new console process group, and
// termination walks the tree via taskkill /T (graceful, then /F).
package processgroup

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
)

const defaultPollPeriod = 25 * time.Millisecond

// Options configures process-group cleanup. Kill and CurrentGroupID exist so
// tests can stub signaling; on Windows Kill receives the (positive) leader PID.
type Options struct {
	Kill           func(pid int, sig syscall.Signal) error
	CurrentGroupID func() int
	PollPeriod     time.Duration
}

func (o Options) kill(pid int, sig syscall.Signal) error {
	if o.Kill != nil {
		return o.Kill(pid, sig)
	}
	return taskkillTree(pid, sig == syscall.SIGKILL)
}

func (o Options) currentGroupID() int {
	if o.CurrentGroupID != nil {
		return o.CurrentGroupID()
	}
	return os.Getpid()
}

func (o Options) pollPeriod() time.Duration {
	if o.PollPeriod > 0 {
		return o.PollPeriod
	}
	return defaultPollPeriod
}

// StartCommandInNewGroup configures cmd to start as a new process-group leader.
func StartCommandInNewGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// Terminate gracefully kills the tree rooted at pgid, waits for exit, then
// escalates to a forced kill.
func Terminate(pgid int, timeout time.Duration, opts Options) error {
	if pgid <= 1 || pgid == opts.currentGroupID() {
		return fmt.Errorf("refusing to signal unsafe process group %d", pgid)
	}
	if err := opts.kill(pgid, syscall.SIGTERM); err != nil && alive(pgid, opts) {
		return err
	}
	if err := waitForExit(pgid, timeout, opts); err == nil {
		return nil
	}
	if err := opts.kill(pgid, syscall.SIGKILL); err != nil && alive(pgid, opts) {
		return err
	}
	return waitForExit(pgid, timeout, opts)
}

// TerminateCommand terminates cmd's process tree and falls back to direct kill.
func TerminateCommand(cmd *exec.Cmd, knownPGID int, timeout time.Duration, opts Options) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pgid := knownPGID
	if pgid <= 0 {
		// The leader is the child itself — Windows has no getpgid.
		pgid = cmd.Process.Pid
	}
	if err := Terminate(pgid, timeout, opts); err != nil {
		return killDirect(cmd.Process, fmt.Errorf("terminate process group %d: %w", pgid, err))
	}
	return nil
}

func waitForExit(pgid int, timeout time.Duration, opts Options) error {
	deadline := time.Now().Add(timeout)
	for {
		if !alive(pgid, opts) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process group %d did not exit within %s", pgid, timeout)
		}
		time.Sleep(opts.pollPeriod())
	}
}

func alive(pgid int, opts Options) bool {
	if pgid <= 0 {
		return false
	}
	return pidutil.Alive(pgid)
}

func killDirect(process *os.Process, cause error) error {
	if err := process.Kill(); err != nil && err != os.ErrProcessDone {
		if cause != nil {
			return fmt.Errorf("%w; direct kill failed: %w", cause, err)
		}
		return err
	}
	return cause
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
