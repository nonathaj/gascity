package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime/herdr"
	"github.com/gastownhall/gascity/internal/runtime/herdr/herdrtest"
	"github.com/gastownhall/gascity/internal/session"
)

// herdrPollerFixture is a city with one live herdr session and a nudge target
// fenced to it, as `gc nudge poll` resolves one.
type herdrPollerFixture struct {
	dir    string
	stub   *herdrtest.Stub
	sp     *herdr.Provider
	store  beads.Store
	target nudgeTarget
}

const herdrPollerSession = "fed__scout"

func newHerdrPollerFixture(t *testing.T, status string) *herdrPollerFixture {
	t.Helper()
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	stub := herdrtest.New(t, herdrPollerSession, status)
	sp := stub.Provider(t.TempDir(), dir)
	store := openNudgeBeadStore(dir).Store
	sess, err := store.Create(beads.Bead{
		Title:  "Scout",
		Type:   session.BeadType,
		Status: "open",
		Labels: []string{session.LabelSession},
		Metadata: map[string]string{
			"alias":        "scout",
			"session_name": herdrPollerSession,
			"provider":     "claude",
		},
	})
	if err != nil {
		t.Fatalf("creating the session bead: %v", err)
	}
	if err := sp.SetMeta(herdrPollerSession, "GC_SESSION_ID", sess.ID); err != nil {
		t.Fatalf("SetMeta(GC_SESSION_ID): %v", err)
	}
	return &herdrPollerFixture{
		dir:   dir,
		stub:  stub,
		sp:    sp,
		store: store,
		target: nudgeTarget{
			cityPath:    dir,
			agent:       config.Agent{Name: "scout"},
			sessionID:   sess.ID,
			resolved:    &config.ResolvedProvider{Name: "claude"},
			sessionName: herdrPollerSession,
		},
	}
}

// enqueue queues one due nudge fenced to the fixture's live session.
func (f *herdrPollerFixture) enqueue(t *testing.T, message string) {
	t.Helper()
	item := newQueuedNudgeWithOptions("scout", message, "session", time.Now().Add(-time.Minute), queuedNudgeOptions{SessionID: f.target.sessionID})
	if err := enqueueQueuedNudge(f.dir, item); err != nil {
		t.Fatalf("enqueueQueuedNudge: %v", err)
	}
}

// tick is one pass of cmdNudgePoll's loop body: observe, then try to deliver.
func (f *herdrPollerFixture) tick(t *testing.T) bool {
	t.Helper()
	obs, err := workerObserveNudgeTarget(f.target, f.store, f.sp)
	if err != nil {
		t.Fatalf("workerObserveNudgeTarget: %v", err)
	}
	if !obs.Running {
		t.Fatalf("observation says the session is not running; the stub should report it live: %+v", obs)
	}
	delivered, err := tryDeliverQueuedNudgesByPoller(f.target, f.store, f.store, f.sp, defaultNudgePollQuiescence, obs)
	if err != nil {
		t.Fatalf("tryDeliverQueuedNudgesByPoller: %v", err)
	}
	return delivered
}

func (f *herdrPollerFixture) queue(t *testing.T) (pending, inFlight, dead int) {
	t.Helper()
	p, i, d, err := listQueuedNudgesForTarget(f.dir, f.target, time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudgesForTarget: %v", err)
	}
	return len(p), len(i), len(d)
}

// TestNudgePoller_HerdrDonePaneQueuedNudgeIsDelivered is the reproduction of
// gf-pdx7a8: a nudge queued to a session parked at a prompt after a finished
// turn (herdr status "done") must be delivered by the poller.
//
// It drives the real herdr Provider over a stub herdr that answers in the
// 0.9.0 shape on the CLI and on the socket. The socket matters: the activity
// tracker reads agent.list there, and with no socket LastActivity would be
// nil because the poll failed, not because of the key mismatch production
// has. The test logs what the gate saw, so the run records the cause as well
// as the symptom.
func TestNudgePoller_HerdrDonePaneQueuedNudgeIsDelivered(t *testing.T) {
	f := newHerdrPollerFixture(t, "done")
	f.enqueue(t, "review queued work")

	// What the gate will see, logged before the poller runs.
	obs, err := workerObserveNudgeTarget(f.target, f.store, f.sp)
	if err != nil {
		t.Fatalf("workerObserveNudgeTarget: %v", err)
	}
	t.Logf("cause evidence: obs.Running=%v obs.LastActivity=%v tracker keys=%q (session name asked for: %q) CanReportActivity=%v",
		obs.Running, obs.LastActivity, f.sp.ActivityKeys(), herdrPollerSession, f.sp.Capabilities().CanReportActivity)

	const ticks = 3
	delivered := 0
	for i := 0; i < ticks; i++ {
		if f.tick(t) {
			delivered++
		}
	}
	t.Logf("cause evidence: `agent wait` calls reached by the gate: %q", f.stub.Calls("agent wait"))

	if got := f.stub.SendCount(); got != 1 {
		t.Errorf("sends to the pane = %d after %d poller ticks, want 1: a done pane is at a prompt", got, ticks)
	}
	if delivered != 1 {
		t.Errorf("ticks that delivered = %d, want 1", delivered)
	}
	if pending, inFlight, dead := f.queue(t); pending != 0 || inFlight != 0 || dead != 0 {
		t.Errorf("queue pending/inFlight/dead = %d/%d/%d, want 0/0/0", pending, inFlight, dead)
	}
}
