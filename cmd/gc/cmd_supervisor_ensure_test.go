package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The keepalive parity contract (gw-x1k, D4 of engdocs/design/windows-systemd-parity.md).
//
// systemd expresses "restart the supervisor, but NOT when it exited because another
// supervisor already owns the port" as Restart=always plus RestartPreventExitStatus=3.
// Windows Task Scheduler has restart-on-failure but cannot inspect an exit code, so a task
// configured to restart would crash-loop a port collision forever, hammering a port that is
// working correctly for somebody else.
//
// The parity mechanism keeps that judgment in gc: a repeating task runs `gc supervisor
// ensure`, which respawns only when the supervisor is genuinely absent AND no recent port
// collision says it should stay absent.

// TestSupervisorEnsureNoopWhenSupervisorAnswers pins the first and most important property:
// a keepalive that runs every few minutes must do NOTHING in the overwhelmingly common case.
func TestSupervisorEnsureNoopWhenSupervisorAnswers(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("GC_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	spawned := false
	code := doSupervisorEnsureWith(&stdout, &stderr,
		func() int { return 4242 }, // supervisor answers
		func() error { spawned = true; return nil },
	)
	if code != 0 {
		t.Fatalf("ensure exit = %d, want 0 when the supervisor answers", code)
	}
	if spawned {
		t.Fatal("ensure respawned a supervisor that was already answering; a keepalive that " +
			"starts a second supervisor is worse than one that does nothing")
	}
}

// TestSupervisorEnsureRefusesRespawnWhileCollisionSentinelIsFresh is the
// RestartPreventExitStatus=3 analog, and the reason this command exists at all.
func TestSupervisorEnsureRefusesRespawnWhileCollisionSentinelIsFresh(t *testing.T) {
	setTestHome(t, t.TempDir())
	home := t.TempDir()
	t.Setenv("GC_HOME", home)

	if err := recordSupervisorPortCollision(); err != nil {
		t.Fatalf("recordSupervisorPortCollision: %v", err)
	}

	var stdout, stderr bytes.Buffer
	spawned := false
	code := doSupervisorEnsureWith(&stdout, &stderr,
		func() int { return 0 }, // absent
		func() error { spawned = true; return nil },
	)
	if code != 0 {
		t.Fatalf("ensure exit = %d, want 0: a suppressed respawn is the designed outcome, "+
			"not a failure the task scheduler should report", code)
	}
	if spawned {
		t.Fatal("ensure respawned while a fresh port-collision sentinel was present; that is " +
			"exactly the crash-loop RestartPreventExitStatus=3 prevents on systemd")
	}
	if !strings.Contains(stderr.String(), "port collision") {
		t.Fatalf("stderr = %q, want it to name the port collision so an operator can see why "+
			"the supervisor is not being restarted", stderr.String())
	}
}

// TestSupervisorEnsureRespawnsWhenAbsentAndNoCollision covers the case the keepalive exists
// for: the supervisor died for an ordinary reason and nothing says it should stay dead.
func TestSupervisorEnsureRespawnsWhenAbsentAndNoCollision(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("GC_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	spawned := false
	code := doSupervisorEnsureWith(&stdout, &stderr,
		func() int { return 0 },
		func() error { spawned = true; return nil },
	)
	if code != 0 {
		t.Fatalf("ensure exit = %d, want 0", code)
	}
	if !spawned {
		t.Fatal("ensure did not respawn an absent supervisor with no collision recorded; the " +
			"keepalive would never bring the fleet back after an ordinary crash")
	}
}

// TestSupervisorPortCollisionSentinelExpires pins that the suppression is TEMPORARY.
//
// A sentinel that never expires converts one port collision into a permanently dead
// supervisor: the operator fixes the conflict, and nothing ever restarts. systemd's
// RestartPreventExitStatus suppresses only the restart of THAT exit, not all future ones.
func TestSupervisorPortCollisionSentinelExpires(t *testing.T) {
	setTestHome(t, t.TempDir())
	home := t.TempDir()
	t.Setenv("GC_HOME", home)

	if err := recordSupervisorPortCollision(); err != nil {
		t.Fatalf("recordSupervisorPortCollision: %v", err)
	}
	// Age the sentinel past its window.
	stale := time.Now().Add(-2 * supervisorPortCollisionWindow)
	if err := os.Chtimes(supervisorPortCollisionSentinelPath(), stale, stale); err != nil {
		t.Fatalf("age sentinel: %v", err)
	}

	if supervisorPortCollisionFresh() {
		t.Fatalf("sentinel still fresh after aging it %s past a %s window; suppression must "+
			"expire or one collision disables the supervisor forever",
			2*supervisorPortCollisionWindow, supervisorPortCollisionWindow)
	}

	var stdout, stderr bytes.Buffer
	spawned := false
	if code := doSupervisorEnsureWith(&stdout, &stderr,
		func() int { return 0 },
		func() error { spawned = true; return nil },
	); code != 0 {
		t.Fatalf("ensure exit = %d, want 0", code)
	}
	if !spawned {
		t.Fatal("ensure still refused to respawn after the sentinel expired")
	}
}

// TestClearSupervisorPortCollisionRestoresKeepaliveCoverage pins that a successful port bind
// ends the suppression immediately rather than leaving it to expire.
//
// This is the difference between suppression tied to the CONDITION and suppression tied to
// the clock. Once a supervisor has actually bound the API port, no other supervisor owns it,
// so the recorded collision is provably over — and an operator who has just fixed a conflict
// should get keepalive coverage back at once, not after the remainder of the window.
func TestClearSupervisorPortCollisionRestoresKeepaliveCoverage(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("GC_HOME", t.TempDir())

	if err := recordSupervisorPortCollision(); err != nil {
		t.Fatalf("recordSupervisorPortCollision: %v", err)
	}
	if !supervisorPortCollisionFresh() {
		t.Fatal("sentinel not fresh immediately after being recorded")
	}

	var clearErr bytes.Buffer
	clearSupervisorPortCollision(&clearErr)
	if clearErr.Len() != 0 {
		t.Fatalf("clearSupervisorPortCollision wrote %q, want silence on the success path", clearErr.String())
	}
	if supervisorPortCollisionFresh() {
		t.Fatal("sentinel still fresh after a successful bind cleared it")
	}

	var stdout, stderr bytes.Buffer
	spawned := false
	if code := doSupervisorEnsureWith(&stdout, &stderr,
		func() int { return 0 },
		func() error { spawned = true; return nil },
	); code != 0 {
		t.Fatalf("ensure exit = %d, want 0", code)
	}
	if !spawned {
		t.Fatal("ensure still refused to respawn after the collision was cleared by a successful bind")
	}
}

// TestClearSupervisorPortCollisionIsQuietWhenAbsent pins that the overwhelmingly common case
// — every clean supervisor start, where no collision was ever recorded — produces no output.
// A warning on every start would train operators to ignore the one that matters.
func TestClearSupervisorPortCollisionIsQuietWhenAbsent(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("GC_HOME", t.TempDir())

	var stderr bytes.Buffer
	clearSupervisorPortCollision(&stderr)
	if stderr.Len() != 0 {
		t.Fatalf("clearSupervisorPortCollision wrote %q when no sentinel existed, want silence", stderr.String())
	}
}

// TestSupervisorPortCollisionSentinelIsPerGCHome pins the isolation that makes this safe to
// use from a shared keepalive task: one isolated home's collision must not suppress another
// home's supervisor.
func TestSupervisorPortCollisionSentinelIsPerGCHome(t *testing.T) {
	setTestHome(t, t.TempDir())
	first := t.TempDir()
	second := t.TempDir()

	t.Setenv("GC_HOME", first)
	if err := recordSupervisorPortCollision(); err != nil {
		t.Fatalf("recordSupervisorPortCollision: %v", err)
	}
	firstPath := supervisorPortCollisionSentinelPath()

	t.Setenv("GC_HOME", second)
	secondPath := supervisorPortCollisionSentinelPath()
	if firstPath == secondPath {
		t.Fatalf("sentinel path %q is shared across GC_HOMEs; one isolated home's collision "+
			"would suppress every other home's supervisor", firstPath)
	}
	if supervisorPortCollisionFresh() {
		t.Fatal("a collision recorded under a different GC_HOME suppressed this one")
	}
	if _, err := os.Stat(filepath.Join(first, filepath.Base(firstPath))); err == nil {
		// Sanity: the first sentinel really was written somewhere under the first home.
		return
	}
}
