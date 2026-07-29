package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/gastownhall/gascity/internal/supervisor"
)

// supervisorPortCollisionWindow is how long a recorded port collision suppresses a keepalive
// respawn.
//
// It bounds a trade-off in both directions. Too short and the keepalive resurrects a
// duplicate supervisor that will collide again, hammering a port another supervisor is using
// correctly. Too long and a collision the operator has since FIXED keeps the supervisor down,
// turning a transient conflict into an outage — systemd's RestartPreventExitStatus suppresses
// the restart of that one exit, never all future ones, and this has to expire to match.
//
// Fifteen minutes is three keepalive periods at the design's five-minute trigger: long enough
// that a collision is not retried immediately, short enough that a fixed conflict recovers
// without operator action.
const supervisorPortCollisionWindow = 15 * time.Minute

// supervisorPortCollisionSentinelPath returns the per-GC_HOME marker recording that this
// supervisor exited because another one already owned the API port.
//
// It lives under the supervisor runtime dir, so it is per-GC_HOME by construction: an
// isolated home's collision must never suppress the real supervisor, and a keepalive task
// shared across homes would otherwise take one home's conflict as a reason to leave every
// other home down.
func supervisorPortCollisionSentinelPath() string {
	return filepath.Join(supervisor.RuntimeDir(), "supervisor-port-collision")
}

// recordSupervisorPortCollision marks that this process is exiting with
// supervisorExitCodePortInUse, so a later keepalive can tell "the supervisor is missing
// because it crashed" from "the supervisor is missing because it deliberately stood down".
//
// The distinction is the whole point. Only the second must suppress a respawn, and Windows
// Task Scheduler cannot make it — it restarts on failure without being able to see an exit
// code — so gc records the reason itself.
func recordSupervisorPortCollision() error {
	dir := supervisor.RuntimeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating supervisor runtime dir: %w", err)
	}
	path := supervisorPortCollisionSentinelPath()
	if err := os.WriteFile(path, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing port-collision sentinel %q: %w", path, err)
	}
	return nil
}

// supervisorPortCollisionFresh reports whether a port collision was recorded recently enough
// to still suppress a respawn.
//
// Freshness is read from the file's MTIME rather than its contents: the timestamp inside is
// for humans reading the file, and deriving behavior from it would make a truncated or
// hand-edited sentinel change what gc does. A missing or unreadable sentinel is "not fresh",
// so any doubt resolves toward restarting the supervisor rather than leaving the fleet down.
func supervisorPortCollisionFresh() bool {
	info, err := os.Stat(supervisorPortCollisionSentinelPath())
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < supervisorPortCollisionWindow
}

// newSupervisorEnsureCmd builds `gc supervisor ensure`, the keepalive entry point.
//
// It is separate from `gc supervisor start` because the two answer different questions.
// start is an operator saying "run the supervisor now" and reports failure when it cannot;
// ensure is a timer asking "should one be running, and is it?" — it must be silent and
// successful in the common case, and it must decline to act when a recent port collision
// says another supervisor already owns the port.
func newSupervisorEnsureCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "ensure",
		Short: "Start the supervisor only if it should be running and is not",
		Long: `Start the machine-wide supervisor only if it should be running and is not.

Intended for a repeating scheduled trigger (the Windows Task Scheduler analog of
systemd's Restart=always). It is idempotent and safe to run every few minutes:

  - exits immediately when the control socket already answers
  - declines to respawn while a recent port collision shows another supervisor
    owns the API port (the RestartPreventExitStatus=3 analog)
  - otherwise starts "gc supervisor run" detached

Declining to respawn is a success, not a failure: a scheduled task should not report
an error on a machine where the supervisor is deliberately not running.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if doSupervisorEnsure(stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
}

// clearSupervisorPortCollision removes the sentinel after a supervisor successfully binds
// the API port, which is proof that no other supervisor owns it any more.
//
// Clearing on success is what keeps the suppression tied to the CONDITION rather than to the
// clock: an operator who resolves the conflict gets keepalive coverage back immediately
// instead of waiting out the remainder of the window.
//
// A missing sentinel is the normal case, so it is not worth reporting; any other failure is,
// because a sentinel that cannot be removed would suppress respawns until it expires.
func clearSupervisorPortCollision(stderr io.Writer) {
	if err := os.Remove(supervisorPortCollisionSentinelPath()); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "gc supervisor: clearing stale port-collision sentinel: %v\n", err) //nolint:errcheck
	}
}

// doSupervisorEnsure is the idempotent keepalive entry point: start the supervisor if, and
// only if, it should be running and is not.
//
// This is the Restart=always analog for hosts that cannot express it. systemd restarts the
// unit unless it exited 3; Task Scheduler can restart on failure but cannot inspect an exit
// code, so a scheduled restart would crash-loop a port collision. Running this from a
// repeating task keeps the judgment in gc, where the exit code is known.
func doSupervisorEnsure(stdout, stderr io.Writer) int {
	return doSupervisorEnsureWith(stdout, stderr, supervisorAlive, ensureStartDetachedSupervisor)
}

// doSupervisorEnsureWith is doSupervisorEnsure with its two side effects injected, so the
// decision logic is testable without a live supervisor or a spawned process.
func doSupervisorEnsureWith(stdout, stderr io.Writer, alive func() int, spawn func() error) int {
	if pid := alive(); pid != 0 {
		fmt.Fprintf(stdout, "gc supervisor: already running (PID %d)\n", pid) //nolint:errcheck
		return 0
	}
	if supervisorPortCollisionFresh() {
		// Exit 0, not an error: standing down is the CORRECT outcome here, and a failing
		// keepalive would show up as a red scheduled task on a machine where nothing is
		// wrong. The reason goes to stderr so an operator asking "why is it not running"
		// gets an answer.
		fmt.Fprintf(stderr, //nolint:errcheck
			"gc supervisor: not restarting — another supervisor owns the API port "+
				"(port collision recorded within %s; see %s)\n",
			supervisorPortCollisionWindow, supervisorPortCollisionSentinelPath())
		return 0
	}
	if err := spawn(); err != nil {
		fmt.Fprintf(stderr, "gc supervisor: ensure failed to start supervisor: %v\n", err) //nolint:errcheck
		return 1
	}
	fmt.Fprintln(stdout, "gc supervisor: started") //nolint:errcheck
	return 0
}

// ensureStartDetachedSupervisor spawns `gc supervisor run` detached, reusing the same path
// `gc start` uses so a keepalive-started supervisor is indistinguishable from one started
// interactively.
func ensureStartDetachedSupervisor() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving gc binary: %w", err)
	}
	logFile, err := supervisorDetachedLogFile()
	if err != nil {
		return err
	}
	defer logFile.Close() //nolint:errcheck
	return startDetached(logFile, exe, "supervisor", "run")
}

// supervisorDetachedLogFile opens the append-only log a keepalive-started supervisor writes
// to. Without it the spawned process would inherit the scheduled task's discarded stdio and
// any startup failure would vanish.
func supervisorDetachedLogFile() (*os.File, error) {
	dir := supervisor.RuntimeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating supervisor runtime dir: %w", err)
	}
	path := filepath.Join(dir, "supervisor-ensure.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %q: %w", path, err)
	}
	return f, nil
}
