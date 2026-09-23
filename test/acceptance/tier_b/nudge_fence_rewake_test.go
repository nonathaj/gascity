//go:build acceptance_b

// Tier B nudge fence re-wake acceptance test.
//
// A queued nudge is fenced to its target session's id and continuation epoch.
// This test queues a nudge for a stopped fresh-mode session, wakes the same
// session with a bumped continuation epoch, and requires the new incarnation's
// prompt-submit hook drain to deliver the nudge. A build that claims the
// stale-epoch item and then rejects it on the fence dead-letters it on the
// first attempt instead, although the sender was told it was queued.
//
// The claim and split decision tables are owned by the cmd/gc unit tests. This
// test owns their composition: the CLI enqueue against a stopped session, the
// controller's suspend drain, the pre-wake epoch commit, the woken process's
// runtime environment, and the hook drain's delivery.
//
// Requires: gc binary, subprocess provider.
// Does NOT require: tmux, dolt, inference API keys.
package tierb_test

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

const (
	// nudgeFenceRewakeAgent is the agent template and the alias of the named
	// session that reserves it.
	nudgeFenceRewakeAgent = "worker"
	// nudgeFenceRewakeLog is the file, under the city's .gc/reports, that the
	// session script appends one START and one DONE line to per incarnation.
	nudgeFenceRewakeLog = "incarnations.log"
	// nudgeFenceRewakeSleeper is the process name each incarnation idles
	// under, so leftover processes can be found by name.
	nudgeFenceRewakeSleeper = "nudge-fence-rewake-sleeper"
	// nudgeFenceRewakeStepTimeout bounds each wait on the city: a start, the
	// suspended incarnation's exit, and the woken incarnation's start. It is a
	// safety deadline; every wait returns as soon as its fact is observed.
	nudgeFenceRewakeStepTimeout = 2 * time.Minute
	// nudgeFenceRewakeDiagTimeout bounds each gc call made while collecting
	// failure diagnostics, so a wedged command cannot stall the suite.
	nudgeFenceRewakeDiagTimeout = 15 * time.Second
	// nudgeFenceRewakeLogTailLines is how much of the supervisor log a failure
	// shows.
	nudgeFenceRewakeLogTailLines = 150
)

// nudgeFenceRewakeScript is the session stand-in. Each incarnation records its
// identity, runs the prompt-submit hook drain once as a provider hook would,
// records that the drain returned, then idles so the session stays running.
// Paths are absolute because the session's working directory is not the city
// root. There is no set -e: a failing drain must still write its DONE line.
const nudgeFenceRewakeScript = `#!/bin/bash
R="$GC_CITY/.gc/reports"
mkdir -p "$R"
echo "START epoch=$GC_CONTINUATION_EPOCH session=$GC_SESSION_ID pid=$$ gc=$(command -v gc)" >> "$R/%[1]s"
gc nudge drain --inject > "$R/drain-$GC_CONTINUATION_EPOCH.out" 2> "$R/drain-$GC_CONTINUATION_EPOCH.err"
echo "DONE epoch=$GC_CONTINUATION_EPOCH drain_exit=$?" >> "$R/%[1]s"
exec -a %[2]s sleep 3600
`

// TestNudgeFenceRewake_StaleEpochQueuedNudgeIsDelivered reproduces, end to
// end, a nudge queued for a session that later wakes as the same session with
// a new continuation epoch.
//
// The scenario is deterministic: it pins the stale fence before the wake.
// Suspending the session drains it, and draining a fresh-mode session marks a
// continuation reset without moving the epoch yet. The nudge is queued while
// nothing runs, so no poller starts, and the queue is checked to hold exactly
// that item, fenced to the first incarnation's session and epoch. Only then is
// the session woken; the pre-wake commit consumes the reset and bumps the
// epoch. The new incarnation's hook drain therefore claims an item whose fence
// no longer matches, and must deliver it rather than dead-letter it.
//
// It never passes vacuously: when the woken incarnation is not the same
// session at a higher epoch, it fails as a scenario that did not reproduce the
// incident shape.
func TestNudgeFenceRewake_StaleEpochQueuedNudgeIsDelivered(t *testing.T) {
	c := helpers.NewCity(t, testEnvB)
	c.Init("claude")
	s := newNudgeFenceRewakeScenario(t, c)
	c.StartWithSupervisor()

	// Incarnation 1: its drain ran against an empty queue.
	first := s.waitForCompletedIncarnations(1)[0]
	s.requireBinaryUnderTest(first)
	t.Logf("incarnation 1: session=%s epoch=%d pid=%d gc=%s", first.SessionID, first.Epoch, first.PID, first.GCPath)

	// Stop the session and wait for its process to exit.
	s.mustGC("session", "suspend", nudgeFenceRewakeAgent)
	if !c.WaitForCondition(func() bool {
		return !nudgeFenceRewakeProcessAlive(first.PID)
	}, nudgeFenceRewakeStepTimeout) {
		s.fatalf("incarnation 1 (pid %d) still running %s after gc session suspend", first.PID, nudgeFenceRewakeStepTimeout)
	}

	// Queue while stopped. Queue delivery never wakes the session.
	queued, err := c.GCStdout("session", "nudge", "--delivery=queue", nudgeFenceRewakeAgent, s.probe)
	if err != nil || !strings.HasPrefix(queued, "Queued nudge for") {
		s.fatalf("gc session nudge --delivery=queue: err=%v stdout=%q; want exit 0 and stdout starting with %q", err, queued, "Queued nudge for")
	}
	t.Logf("gc session nudge --delivery=queue: %s", strings.TrimSpace(queued))

	// Pin the stale fence: the probe alone, fenced to incarnation 1.
	pinned := s.nudgeStatus()
	if !pinned.holdsOnlyPending(s.probe, first.SessionID, first.Epoch) {
		s.fatalf("before the wake, want only %q pending, fenced to (session %s, epoch %d); queue: %s",
			s.probe, first.SessionID, first.Epoch, pinned.describe())
	}
	t.Logf("stale fence pinned before the wake: %s", pinned.describe())

	// Wake. The pre-wake commit bumps the epoch.
	s.mustGC("session", "wake", nudgeFenceRewakeAgent)

	second := s.waitForCompletedIncarnations(2)[1]
	s.requireBinaryUnderTest(second)
	t.Logf("incarnation 2: session=%s epoch=%d pid=%d gc=%s", second.SessionID, second.Epoch, second.PID, second.GCPath)
	if second.SessionID != first.SessionID || second.Epoch <= first.Epoch {
		s.fatalf("scenario did not reproduce the incident shape: incarnation 1 was (session %s, epoch %d) and incarnation 2 is (session %s, epoch %d); want the same session at a higher epoch",
			first.SessionID, first.Epoch, second.SessionID, second.Epoch)
	}

	// Delivery: the woken incarnation's drain carried the probe, the queue is
	// empty, and no poller took part.
	drainOut := nudgeFenceRewakeDrainOut(second.Epoch)
	wantLine := "- [session] " + s.probe
	drained := s.report(drainOut)
	final := s.nudgeStatus()
	var failures []string
	if !slices.Contains(strings.Split(drained, "\n"), wantLine) {
		failures = append(failures, fmt.Sprintf("%s has no line %q", drainOut, wantLine))
	}
	if final.Counts != (nudgeFenceRewakeCounts{}) {
		failures = append(failures, fmt.Sprintf("queue counts are pending %d, in-flight %d, dead %d; want 0, 0, 0",
			final.Counts.Pending, final.Counts.InFlight, final.Counts.Dead))
	}
	if final.Session == "" {
		failures = append(failures, "gc nudge status --json named no session, so its poller files cannot be checked")
	} else if pollers := s.pollerFiles(final.Session); len(pollers) > 0 {
		failures = append(failures, fmt.Sprintf("poller files %v for session %s; want none", pollers, final.Session))
	}
	if len(failures) > 0 {
		s.fatalf("the woken incarnation did not deliver the stale-fence nudge:\n  %s\nqueue after the wake: %s",
			strings.Join(failures, "\n  "), final.describe())
	}
	t.Logf("delivered by the incarnation 2 hook drain: %s has %q; queue after the wake: %s", drainOut, wantLine, final.describe())
}

// nudgeFenceRewakeScenario is one city running the scenario.
type nudgeFenceRewakeScenario struct {
	t          *testing.T
	c          *helpers.City
	reportsDir string
	probe      string
}

// newNudgeFenceRewakeScenario writes the session script and the city
// configuration, and picks a unique probe message. The replacement city.toml
// keeps the identity gc init recorded in .gc/site.toml and sets the file beads
// provider and a 1s patrol, so each lifecycle step is picked up promptly; it
// sets no default provider, so the session gc init scaffolded is not started.
// One fresh-mode agent is reserved as an always-on named session. The harness
// environment supplies the subprocess session provider.
func newNudgeFenceRewakeScenario(t *testing.T, c *helpers.City) *nudgeFenceRewakeScenario {
	t.Helper()
	s := &nudgeFenceRewakeScenario{
		t:          t,
		c:          c,
		reportsDir: filepath.Join(c.Dir, ".gc", "reports"),
		probe:      "fence-probe-" + strings.ToLower(rand.Text()),
	}
	scriptPath := filepath.Join(c.Dir, ".gc", "scripts", "nudge-fence-rewake.sh")
	if err := os.MkdirAll(filepath.Dir(scriptPath), 0o755); err != nil {
		t.Fatalf("creating scripts dir: %v", err)
	}
	script := fmt.Sprintf(nudgeFenceRewakeScript, nudgeFenceRewakeLog, nudgeFenceRewakeSleeper)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing session script: %v", err)
	}
	c.WriteConfig(`[beads]
provider = "file"

[daemon]
patrol_interval = "1s"
`)
	c.WriteV2AgentDir(nudgeFenceRewakeAgent,
		fmt.Sprintf("start_command = %q", "bash "+scriptPath),
		`wake_mode = "fresh"`,
		"max_active_sessions = 1",
	)
	c.AppendToPack(fmt.Sprintf("\n[[named_session]]\ntemplate = %q\nscope = \"city\"\nmode = \"always\"\n", nudgeFenceRewakeAgent))
	return s
}

// nudgeFenceRewakeIncarnation is one session start, as its START line
// recorded it.
type nudgeFenceRewakeIncarnation struct {
	Epoch     int
	SessionID string
	PID       int
	GCPath    string
}

// waitForCompletedIncarnations waits until n incarnations have written their
// DONE line and returns them in completion order.
func (s *nudgeFenceRewakeScenario) waitForCompletedIncarnations(n int) []nudgeFenceRewakeIncarnation {
	s.t.Helper()
	var got []nudgeFenceRewakeIncarnation
	var readErr error
	if !s.c.WaitForCondition(func() bool {
		got, readErr = s.completedIncarnations()
		return readErr != nil || len(got) >= n
	}, nudgeFenceRewakeStepTimeout) {
		s.fatalf("want %d completed incarnations within %s; have %d", n, nudgeFenceRewakeStepTimeout, len(got))
	}
	if readErr != nil {
		s.fatalf("reading %s: %v", nudgeFenceRewakeLog, readErr)
	}
	return got
}

// completedIncarnations reads the incarnation log. A missing log means no
// incarnation has started yet.
func (s *nudgeFenceRewakeScenario) completedIncarnations() ([]nudgeFenceRewakeIncarnation, error) {
	data, err := os.ReadFile(filepath.Join(s.reportsDir, nudgeFenceRewakeLog))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseNudgeFenceRewakeLog(string(data))
}

// parseNudgeFenceRewakeLog returns the incarnations whose DONE line has been
// written, in DONE order, each identified by the most recent START line with
// the same epoch. A final line without a newline is still being appended and
// is left for the next read.
func parseNudgeFenceRewakeLog(data string) ([]nudgeFenceRewakeIncarnation, error) {
	end := strings.LastIndexByte(data, '\n')
	if end < 0 {
		return nil, nil
	}
	var started, completed []nudgeFenceRewakeIncarnation
	for i, line := range strings.Split(data[:end], "\n") {
		marker, rest, _ := strings.Cut(line, " ")
		switch marker {
		case "START":
			inc, err := parseNudgeFenceRewakeStart(rest)
			if err != nil {
				return nil, fmt.Errorf("line %d %q: %w", i+1, line, err)
			}
			started = append(started, inc)
		case "DONE":
			epoch, err := strconv.Atoi(nudgeFenceRewakeField(rest, "epoch"))
			if err != nil {
				return nil, fmt.Errorf("line %d %q: epoch: %w", i+1, line, err)
			}
			match := -1
			for j := len(started) - 1; j >= 0; j-- {
				if started[j].Epoch == epoch {
					match = j
					break
				}
			}
			if match < 0 {
				return nil, fmt.Errorf("line %d %q: no START line for epoch %d", i+1, line, epoch)
			}
			completed = append(completed, started[match])
		default:
			return nil, fmt.Errorf("line %d %q: unknown marker", i+1, line)
		}
	}
	return completed, nil
}

// parseNudgeFenceRewakeStart parses the fields after START. The gc path comes
// last and is taken whole.
func parseNudgeFenceRewakeStart(rest string) (nudgeFenceRewakeIncarnation, error) {
	fields, gcPath, _ := strings.Cut(rest, " gc=")
	if gcPath == "" {
		return nudgeFenceRewakeIncarnation{}, errors.New("no gc path: gc was not on the session's PATH")
	}
	epoch, err := strconv.Atoi(nudgeFenceRewakeField(fields, "epoch"))
	if err != nil {
		return nudgeFenceRewakeIncarnation{}, fmt.Errorf("epoch: %w", err)
	}
	pid, err := strconv.Atoi(nudgeFenceRewakeField(fields, "pid"))
	if err != nil {
		return nudgeFenceRewakeIncarnation{}, fmt.Errorf("pid: %w", err)
	}
	sessionID := nudgeFenceRewakeField(fields, "session")
	if sessionID == "" {
		return nudgeFenceRewakeIncarnation{}, errors.New("no session id")
	}
	return nudgeFenceRewakeIncarnation{Epoch: epoch, SessionID: sessionID, PID: pid, GCPath: gcPath}, nil
}

// nudgeFenceRewakeField returns key's value from space-separated key=value
// fields, or "" when the key is absent.
func nudgeFenceRewakeField(fields, key string) string {
	for _, field := range strings.Fields(fields) {
		if k, v, ok := strings.Cut(field, "="); ok && k == key {
			return v
		}
	}
	return ""
}

// nudgeFenceRewakeDrainOut names the hook drain's stdout for an epoch.
func nudgeFenceRewakeDrainOut(epoch int) string {
	return fmt.Sprintf("drain-%d.out", epoch)
}

// requireBinaryUnderTest fails unless the incarnation's gc, after symlink
// resolution, is the binary this suite runs: the GC_ACCEPTANCE_GC_BIN
// override or the harness-built binary.
func (s *nudgeFenceRewakeScenario) requireBinaryUnderTest(inc nudgeFenceRewakeIncarnation) {
	s.t.Helper()
	want := s.c.Env.Get("GC_ACCEPTANCE_GC_BIN")
	wantResolved, wantErr := filepath.EvalSymlinks(want)
	gotResolved, gotErr := filepath.EvalSymlinks(inc.GCPath)
	if wantErr != nil || gotErr != nil || gotResolved != wantResolved {
		s.fatalf("incarnation at epoch %d ran gc %q (resolved %q, error %v); want the binary under test %q (resolved %q, error %v)",
			inc.Epoch, inc.GCPath, gotResolved, gotErr, want, wantResolved, wantErr)
	}
}

// nudgeFenceRewakeProcessAlive reports whether pid still names a live process.
// Signal 0 probes without delivering a signal; os.FindProcess keeps the check
// portable where syscall.Kill does not exist.
func nudgeFenceRewakeProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	alive := p.Signal(syscall.Signal(0)) == nil
	_ = p.Release()
	return alive
}

// mustGC runs a gc command that must succeed and logs its output.
func (s *nudgeFenceRewakeScenario) mustGC(args ...string) {
	s.t.Helper()
	out, err := s.c.GC(args...)
	if err != nil {
		s.fatalf("gc %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	s.t.Logf("gc %s: %s", strings.Join(args, " "), strings.TrimSpace(out))
}

// nudgeFenceRewakeStatus is the part of gc nudge status --json this test reads.
type nudgeFenceRewakeStatus struct {
	Session  string                   `json:"session"`
	Counts   nudgeFenceRewakeCounts   `json:"counts"`
	Pending  []nudgeFenceRewakeQueued `json:"pending"`
	InFlight []nudgeFenceRewakeQueued `json:"in_flight"`
	Dead     []nudgeFenceRewakeQueued `json:"dead"`
}

// nudgeFenceRewakeCounts is the queue's per-bucket item count.
type nudgeFenceRewakeCounts struct {
	Pending  int `json:"pending"`
	InFlight int `json:"in_flight"`
	Dead     int `json:"dead"`
}

// nudgeFenceRewakeQueued is one queued nudge: its fence, message, and
// delivery attempts.
type nudgeFenceRewakeQueued struct {
	SessionID         string `json:"session_id"`
	ContinuationEpoch string `json:"continuation_epoch"`
	Source            string `json:"source"`
	Message           string `json:"message"`
	Attempts          int    `json:"attempts"`
	LastError         string `json:"last_error"`
}

// nudgeStatus reads the agent's queue through gc nudge status --json.
func (s *nudgeFenceRewakeScenario) nudgeStatus() nudgeFenceRewakeStatus {
	s.t.Helper()
	out, err := s.c.GCStdout("nudge", "status", nudgeFenceRewakeAgent, "--json")
	if err != nil {
		s.fatalf("gc nudge status %s --json: %v\n%s", nudgeFenceRewakeAgent, err, out)
	}
	var status nudgeFenceRewakeStatus
	if err := json.Unmarshal([]byte(out), &status); err != nil {
		s.fatalf("decoding gc nudge status --json: %v\n%s", err, out)
	}
	return status
}

// holdsOnlyPending reports whether the queue holds one item, pending, with the
// given message and fence.
func (st nudgeFenceRewakeStatus) holdsOnlyPending(message, sessionID string, epoch int) bool {
	if st.Counts != (nudgeFenceRewakeCounts{Pending: 1}) || len(st.Pending) != 1 {
		return false
	}
	item := st.Pending[0]
	return item.Message == message && item.SessionID == sessionID && item.ContinuationEpoch == strconv.Itoa(epoch)
}

// describe renders the counts and every item's fence and delivery state, so a
// dead letter's reason and attempts show without a second lookup.
func (st nudgeFenceRewakeStatus) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pending %d, in-flight %d, dead %d", st.Counts.Pending, st.Counts.InFlight, st.Counts.Dead)
	for _, bucket := range []struct {
		name  string
		items []nudgeFenceRewakeQueued
	}{{"pending", st.Pending}, {"in-flight", st.InFlight}, {"dead", st.Dead}} {
		for _, item := range bucket.items {
			fmt.Fprintf(&b, "\n    %s: message=%q source=%q session_id=%q continuation_epoch=%q attempts=%d last_error=%q",
				bucket.name, item.Message, item.Source, item.SessionID, item.ContinuationEpoch, item.Attempts, item.LastError)
		}
	}
	return b.String()
}

// report returns a file from the city's reports directory, or why it could
// not be read.
func (s *nudgeFenceRewakeScenario) report(name string) string {
	data, err := os.ReadFile(filepath.Join(s.reportsDir, name))
	if err != nil {
		return fmt.Sprintf("(unreadable: %v)", err)
	}
	return string(data)
}

// pollerFiles lists one session's nudge poller pid and pid-lock files, which
// are named after the session. Starting a poller for the session creates both,
// and the lock is never removed, so an empty list means no poller was ever
// started for it and the hook drain delivered on its own. Files of other
// sessions do not count: the city that gc init scaffolds can start its own
// session before this test's configuration replaces it.
func (s *nudgeFenceRewakeScenario) pollerFiles(sessionName string) []string {
	var files []string
	for _, suffix := range []string{".pid", ".pid.lock"} {
		matches, _ := filepath.Glob(filepath.Join(s.pollersDir(), sessionName+"-*"+suffix))
		files = append(files, matches...)
	}
	return files
}

// pollersDir is the city's nudge poller directory.
func (s *nudgeFenceRewakeScenario) pollersDir() string {
	return filepath.Join(s.c.Dir, ".gc", "nudges", "pollers")
}

// fatalf fails the test with the message followed by the diagnostics bundle.
func (s *nudgeFenceRewakeScenario) fatalf(format string, args ...any) {
	s.t.Helper()
	s.t.Fatalf("%s\n\n%s", fmt.Sprintf(format, args...), s.diagnostics())
}

// diagnostics collects what a failure needs for triage: the queue status,
// every drain output, the incarnation log, the session list, the poller
// directory, and the supervisor log tail.
func (s *nudgeFenceRewakeScenario) diagnostics() string {
	var b strings.Builder
	section := func(title, body string) {
		fmt.Fprintf(&b, "=== %s ===\n%s\n", title, strings.TrimRight(body, "\n"))
	}
	section("gc nudge status "+nudgeFenceRewakeAgent+" --json", s.boundedGC("nudge", "status", nudgeFenceRewakeAgent, "--json"))
	drains, _ := filepath.Glob(filepath.Join(s.reportsDir, "drain-*"))
	sort.Strings(drains)
	for _, path := range drains {
		section(filepath.Base(path), s.report(filepath.Base(path)))
	}
	section(nudgeFenceRewakeLog, s.report(nudgeFenceRewakeLog))
	section("gc session list --state all --json", s.boundedGC("session", "list", "--state", "all", "--json"))
	pollerFiles, _ := filepath.Glob(filepath.Join(s.pollersDir(), "*"))
	section("nudge poller directory", strings.Join(pollerFiles, "\n"))
	section("supervisor.log tail", nudgeFenceRewakeTail(filepath.Join(s.c.Env.Get("GC_HOME"), "supervisor.log")))
	return b.String()
}

// boundedGC runs a diagnostic gc command and returns its combined output, or
// a note that it did not finish within nudgeFenceRewakeDiagTimeout.
func (s *nudgeFenceRewakeScenario) boundedGC(args ...string) string {
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := s.c.GC(args...)
		done <- result{out: out, err: err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return fmt.Sprintf("%s\n(gc exited: %v)", r.out, r.err)
		}
		return r.out
	case <-time.After(nudgeFenceRewakeDiagTimeout):
		return fmt.Sprintf("(gc %s did not finish within %s)", strings.Join(args, " "), nudgeFenceRewakeDiagTimeout)
	}
}

// nudgeFenceRewakeTail returns the last nudgeFenceRewakeLogTailLines lines of
// a log file, or why it could not be read.
func nudgeFenceRewakeTail(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("(unreadable: %v)", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > nudgeFenceRewakeLogTailLines {
		lines = lines[len(lines)-nudgeFenceRewakeLogTailLines:]
	}
	return strings.Join(lines, "\n")
}
