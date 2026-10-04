package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/runtime/auto"
	"github.com/gastownhall/gascity/internal/runtime/herdr"
	"github.com/gastownhall/gascity/internal/runtime/herdr/herdrtest"
	"github.com/gastownhall/gascity/internal/runtime/hybrid"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
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
	delivered, _, err := tryDeliverQueuedNudgesByPoller(f.target, f.store, f.store, f.sp, defaultNudgePollQuiescence, obs)
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

// ── the delivery gate over herdr (gf-pdx7a8) ─────────────────────────────────
//
// The cases below drive the same stub as the reproduction above. None of them
// waits for a timer to decide a verdict, and none uses a context deadline equal
// to a wait timeout: the gate reads the agent's state and decides.

// tickReporting is one pass of cmdNudgePoll's loop body including the hold
// line: observe, try to deliver, report a hold. obs may be adjusted by the
// caller before the gate sees it.
func (f *herdrPollerFixture) tickReporting(t *testing.T, holds *pollerHoldReporter, out *strings.Builder, now time.Time, adjust func(*worker.LiveObservation)) (bool, pollerGateVerdict) {
	t.Helper()
	obs, err := workerObserveNudgeTarget(f.target, f.store, f.sp)
	if err != nil {
		t.Fatalf("workerObserveNudgeTarget: %v", err)
	}
	if adjust != nil {
		adjust(&obs)
	}
	delivered, verdict, err := tryDeliverQueuedNudgesByPoller(f.target, f.store, f.store, f.sp, defaultNudgePollQuiescence, obs)
	if err != nil {
		t.Fatalf("tryDeliverQueuedNudgesByPollerGated: %v", err)
	}
	holds.report(out, "gc nudge poll", f.target.sessionName, verdict, now, func() (int, error) {
		pending, _, _, err := listQueuedNudgesForTarget(f.dir, f.target, time.Now())
		return len(pending), err
	})
	return delivered, verdict
}

func holdLines(out *strings.Builder) []string {
	text := strings.TrimRight(out.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// E1 (the stamp half; delivery is the reproduction above): a delivered nudge
// stamps last_nudge_delivered_at on the session.
func TestNudgePoller_HerdrDonePaneDeliveryStampsTheSession(t *testing.T) {
	f := newHerdrPollerFixture(t, "done")
	f.enqueue(t, "review queued work")
	if !f.tick(t) {
		t.Fatal("delivered = false, want a nudge delivered to a done pane on the first tick")
	}
	sess, err := f.store.Get(f.target.sessionID)
	if err != nil {
		t.Fatalf("reading the session bead: %v", err)
	}
	if got := sess.Metadata[session.MetadataLastNudgeDeliveredAt]; got == "" {
		t.Fatalf("%s is empty after a delivery; metadata = %v", session.MetadataLastNudgeDeliveredAt, sess.Metadata)
	}
	if calls := f.stub.Calls("agent wait"); len(calls) != 0 {
		t.Fatalf("the gate waited on herdr (%q); it must read the state, not wait for idle", calls)
	}
}

// E2: an idle pane (never used, or its input box empty) takes a queued nudge.
func TestNudgePoller_HerdrIdlePaneQueuedNudgeIsDelivered(t *testing.T) {
	f := newHerdrPollerFixture(t, "idle")
	f.enqueue(t, "review queued work")
	if !f.tick(t) {
		t.Fatal("delivered = false, want a nudge delivered to an idle pane")
	}
	if got := f.stub.SendCount(); got != 1 {
		t.Fatalf("sends = %d, want 1", got)
	}
	if pending, inFlight, dead := f.queue(t); pending != 0 || inFlight != 0 || dead != 0 {
		t.Fatalf("queue pending/inFlight/dead = %d/%d/%d, want 0/0/0", pending, inFlight, dead)
	}
}

// E3: every due nudge for a done pane is delivered, and the queue ends empty.
func TestNudgePoller_HerdrDonePaneDeliversEveryDueNudge(t *testing.T) {
	f := newHerdrPollerFixture(t, "done")
	for _, m := range []string{"first reminder", "second reminder", "third reminder"} {
		f.enqueue(t, m)
	}
	for i := 0; i < 3; i++ {
		f.tick(t)
	}
	if got := f.stub.SendCount(); got < 1 {
		t.Fatalf("sends = %d, want at least 1", got)
	}
	if pending, inFlight, dead := f.queue(t); pending != 0 || inFlight != 0 || dead != 0 {
		t.Fatalf("queue pending/inFlight/dead = %d/%d/%d, want 0/0/0: all three delivered", pending, inFlight, dead)
	}
}

// H1: a pane mid-turn holds the nudge and says so; it is delivered once the
// turn finishes.
func TestNudgePoller_HerdrWorkingPaneHoldsThenDeliversWhenDone(t *testing.T) {
	f := newHerdrPollerFixture(t, "working")
	f.enqueue(t, "review queued work")
	var holds pollerHoldReporter
	var out strings.Builder

	delivered, verdict := f.tickReporting(t, &holds, &out, time.Now(), nil)
	if delivered || !verdict.held() {
		t.Fatalf("delivered=%v verdict=%+v, want a hold while the agent is working", delivered, verdict)
	}
	if got := f.stub.SendCount(); got != 0 {
		t.Fatalf("sends = %d, want 0 while working", got)
	}
	want := "gc nudge poll: hold session=" + herdrPollerSession + " reason=not-deliverable status=working pending=1"
	if lines := holdLines(&out); len(lines) != 1 || lines[0] != want {
		t.Fatalf("hold lines = %q, want exactly %q", lines, want)
	}
	if pending, _, _ := f.queue(t); pending != 1 {
		t.Fatalf("pending = %d, want the held nudge still pending", pending)
	}

	f.stub.SetStatus("done")
	delivered, verdict = f.tickReporting(t, &holds, &out, time.Now(), nil)
	if !delivered || verdict.held() {
		t.Fatalf("delivered=%v verdict=%+v, want delivery once the turn is done", delivered, verdict)
	}
	if got := f.stub.SendCount(); got != 1 {
		t.Fatalf("sends = %d, want 1 after the turn finished", got)
	}
}

// H2: a pane sitting on a dialog holds — even with an activity stamp older
// than the quiescence window. A blocked pane's stamp is frozen, so the stamp
// alone reads it as quiet; the state has to be consulted first, or the queued
// text is typed into the dialog.
func TestNudgePoller_HerdrBlockedPaneHoldsDespiteAnOldActivityStamp(t *testing.T) {
	f := newHerdrPollerFixture(t, "blocked")
	f.enqueue(t, "review queued work")
	var holds pollerHoldReporter
	var out strings.Builder
	old := time.Now().Add(-10 * defaultNudgePollQuiescence)

	delivered, verdict := f.tickReporting(t, &holds, &out, time.Now(), func(obs *worker.LiveObservation) { obs.LastActivity = &old })
	if delivered || verdict.reason != pollerHoldNotDeliverable || verdict.status != "blocked" {
		t.Fatalf("delivered=%v verdict=%+v, want hold reason=%s status=blocked", delivered, verdict, pollerHoldNotDeliverable)
	}
	if got := f.stub.SendCount(); got != 0 {
		t.Fatalf("sends = %d, want 0: input sent now would answer the dialog", got)
	}
	if lines := holdLines(&out); len(lines) != 1 || !strings.Contains(lines[0], "status=blocked") {
		t.Fatalf("hold lines = %q, want one naming status=blocked", lines)
	}
	// The stamp alone would have delivered: that is the order this pins.
	if !pollerSessionIdleEnough(f.target, f.sp, defaultNudgePollQuiescence, worker.LiveObservation{Running: true, LastActivity: &old}) {
		t.Fatal("the activity-stamp path no longer reads an old stamp as quiet; this test's premise changed")
	}
}

// H3 and H3b: a pane herdr cannot classify, and a status word this gate has
// never heard of, both hold. A new herdr state is not pasted into on a guess.
func TestNudgePoller_HerdrUnknownAndUnrecognizedStatusHold(t *testing.T) {
	for _, status := range []string{"unknown", "compacting"} {
		t.Run(status, func(t *testing.T) {
			f := newHerdrPollerFixture(t, status)
			f.enqueue(t, "review queued work")
			var holds pollerHoldReporter
			var out strings.Builder
			delivered, verdict := f.tickReporting(t, &holds, &out, time.Now(), nil)
			if delivered || verdict.reason != pollerHoldNotDeliverable || verdict.status != status {
				t.Fatalf("delivered=%v verdict=%+v, want hold reason=%s status=%s", delivered, verdict, pollerHoldNotDeliverable, status)
			}
			if got := f.stub.SendCount(); got != 0 {
				t.Fatalf("sends = %d, want 0", got)
			}
			if lines := holdLines(&out); len(lines) != 1 || !strings.Contains(lines[0], "status="+status) {
				t.Fatalf("hold lines = %q, want one naming status=%s", lines, status)
			}
		})
	}
}

// H4: when herdr cannot be asked, nothing is known, so nothing is delivered —
// and the hold says could-not-look, not a state. Before this gate a herdr
// transport error failed the wait fast, and a fast failure read as "idle".
func TestNudgePoller_HerdrUnreachableHoldsAsCouldNotLook(t *testing.T) {
	f := newHerdrPollerFixture(t, "done")
	f.enqueue(t, "review queued work")
	f.stub.SetStatus("down")
	// The poller observes before it gates, and an unreachable herdr reads as
	// not running there. The gate is asked directly, as it is when herdr goes
	// away between the observation and the gate.
	obs := worker.LiveObservation{Running: true}

	verdict := pollerDeliveryGate(f.target, f.sp, defaultNudgePollQuiescence, obs)
	if verdict.deliver || verdict.reason != pollerHoldCouldNotLook || verdict.err == nil {
		t.Fatalf("verdict = %+v, want hold reason=%s with the error", verdict, pollerHoldCouldNotLook)
	}
	delivered, verdict, err := tryDeliverQueuedNudgesByPoller(f.target, f.store, f.store, f.sp, defaultNudgePollQuiescence, obs)
	if err != nil {
		t.Fatalf("tryDeliverQueuedNudgesByPollerGated: %v", err)
	}
	if delivered || verdict.reason != pollerHoldCouldNotLook {
		t.Fatalf("delivered=%v verdict=%+v, want a could-not-look hold", delivered, verdict)
	}
	var holds pollerHoldReporter
	var out strings.Builder
	holds.report(&out, "gc nudge poll", f.target.sessionName, verdict, time.Now(), func() (int, error) { return 1, nil })
	lines := holdLines(&out)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "gc nudge poll: hold session="+herdrPollerSession+" reason=could-not-look status=none pending=1 error=") {
		t.Fatalf("hold lines = %q, want one could-not-look line carrying the error", lines)
	}
	f.stub.SetStatus("done")
	if got := f.stub.SendCount(); got != 0 {
		t.Fatalf("sends = %d, want 0", got)
	}
	if pending, inFlight, dead := f.queue(t); pending != 1 || inFlight != 0 || dead != 0 {
		t.Fatalf("queue pending/inFlight/dead = %d/%d/%d, want 1/0/0: held, not dropped", pending, inFlight, dead)
	}
}

// H5: with no registered agent (a raw shell pane) there is no state to read,
// and the gate gives exactly the answer it gave before readiness existed.
func TestNudgePoller_HerdrNoRegisteredAgentKeepsTheOldGate(t *testing.T) {
	f := newHerdrPollerFixture(t, "absent")
	obs := worker.LiveObservation{Running: true}

	old := pollerSessionIdleEnough(f.target, f.sp, defaultNudgePollQuiescence, obs)
	verdict := pollerDeliveryGate(f.target, f.sp, defaultNudgePollQuiescence, obs)
	if verdict.deliver != old {
		t.Fatalf("gate deliver = %v, the pre-readiness gate says %v; no_agent must fall through unchanged", verdict.deliver, old)
	}
	// And the old expectation itself, unchanged: herdr's WaitForIdle treats an
	// unregistered session as "the caller may proceed".
	if !old {
		t.Fatal("pollerSessionIdleEnough = false for an unregistered herdr session, want true as on b66ac5697")
	}
	if verdict.status != "" || verdict.reason != "" {
		t.Fatalf("verdict = %+v, want a plain deliver with no readiness status", verdict)
	}
}

// R1: an unchanged hold is one line, not one per tick; it repeats after the
// interval; a changed status is a new line; and nothing is written when
// nothing is pending.
func TestNudgePoller_HoldLineIsWrittenOncePerChange(t *testing.T) {
	f := newHerdrPollerFixture(t, "working")
	var holds pollerHoldReporter
	var out strings.Builder
	base := time.Now()

	// Nothing queued: a held session with nothing to hold says nothing.
	f.tickReporting(t, &holds, &out, base, nil)
	if lines := holdLines(&out); len(lines) != 0 {
		t.Fatalf("hold lines with an empty queue = %q, want none", lines)
	}

	f.enqueue(t, "review queued work")
	for i := 0; i < 5; i++ {
		f.tickReporting(t, &holds, &out, base.Add(time.Duration(i)*time.Second), nil)
	}
	if lines := holdLines(&out); len(lines) != 1 {
		t.Fatalf("hold lines after 5 ticks at one status = %d (%q), want 1", len(lines), lines)
	}

	f.tickReporting(t, &holds, &out, base.Add(pollerHoldLogInterval+5*time.Second), nil)
	if lines := holdLines(&out); len(lines) != 2 {
		t.Fatalf("hold lines after the repeat interval = %d (%q), want 2", len(lines), lines)
	}

	f.stub.SetStatus("blocked")
	f.tickReporting(t, &holds, &out, base.Add(pollerHoldLogInterval+6*time.Second), nil)
	lines := holdLines(&out)
	if len(lines) != 3 || !strings.Contains(lines[2], "status=blocked") {
		t.Fatalf("hold lines after the status changed = %q, want a third naming status=blocked", lines)
	}
	if got := f.stub.SendCount(); got != 0 {
		t.Fatalf("sends = %d, want 0 across every held tick", got)
	}
}

// W1: the capability has to survive the wrappers production puts around the
// backend, or the gate's type assertion sees only the wrapper and the fix is
// dead code. Asserted on what the production constructor returns for a herdr
// city, and on auto and hybrid routing to a herdr backend.
func TestNudgeReadinessReachesTheBackendThroughProductionWrappers(t *testing.T) {
	t.Run("the provider production builds for a herdr city", func(t *testing.T) {
		t.Setenv("GC_BEADS", "file")
		dir := t.TempDir()
		cfg := &config.City{Workspace: config.Workspace{Name: "wrapcity"}, Session: config.SessionConfig{Provider: "herdr"}}
		sp, err := newSessionProviderForCity(cfg, dir)
		if err != nil {
			t.Fatalf("newSessionProviderForCity: %v", err)
		}
		if _, ok := sp.(runtime.NudgeReadinessProvider); !ok {
			t.Fatalf("%T, built for a herdr city, does not expose NudgeReadiness", sp)
		}
	})

	stub := herdrtest.New(t, herdrPollerSession, "done")
	backend := stub.Provider(t.TempDir(), t.TempDir())
	target := nudgeTarget{sessionName: herdrPollerSession}
	for name, sp := range map[string]runtime.Provider{
		"auto":   auto.New(backend, runtime.NewFake()),
		"hybrid": hybrid.New(backend, runtime.NewFake(), func(string) bool { return false }),
	} {
		t.Run(name+" over herdr", func(t *testing.T) {
			rp, ok := sp.(runtime.NudgeReadinessProvider)
			if !ok {
				t.Fatalf("%T does not expose NudgeReadiness", sp)
			}
			readiness, status, err := rp.NudgeReadiness(t.Context(), herdrPollerSession)
			if err != nil || readiness != runtime.NudgeReady || status != "done" {
				t.Fatalf("NudgeReadiness = %q, %q, %v; want ready, done, nil", readiness, status, err)
			}
			if v := pollerDeliveryGate(target, sp, defaultNudgePollQuiescence, worker.LiveObservation{Running: true}); !v.deliver || v.status != "done" {
				t.Fatalf("gate verdict through %s = %+v, want deliver with status done", name, v)
			}
		})
	}
	for name, sp := range map[string]runtime.Provider{
		"auto":   auto.New(runtime.NewFake(), runtime.NewFake()),
		"hybrid": hybrid.New(runtime.NewFake(), runtime.NewFake(), func(string) bool { return false }),
	} {
		t.Run(name+" over a backend without the capability", func(t *testing.T) {
			_, _, err := sp.(runtime.NudgeReadinessProvider).NudgeReadiness(t.Context(), "sess-worker")
			if !errors.Is(err, runtime.ErrInteractionUnsupported) {
				t.Fatalf("NudgeReadiness error = %v, want ErrInteractionUnsupported", err)
			}
		})
	}
}

// W2: for a provider without the capability the gate is the old gate. Each
// case is one of the existing pollerSessionIdleEnough expectations, run
// through both functions.
func TestNudgePoller_GateIsUnchangedForProvidersWithoutReadiness(t *testing.T) {
	oldEnough := time.Now().Add(-5 * time.Second)
	tooRecent := time.Now().Add(-1 * time.Second)
	target := nudgeTarget{sessionName: "sess-worker"}

	started := func(t *testing.T) *runtime.Fake {
		t.Helper()
		fake := runtime.NewFake()
		if err := fake.Start(t.Context(), "sess-worker", runtime.Config{}); err != nil {
			t.Fatalf("Start: %v", err)
		}
		return fake
	}
	cases := []struct {
		name string
		sp   func(t *testing.T) runtime.Provider
		obs  worker.LiveObservation
		want bool
	}{
		{"nil provider, activity old enough", func(*testing.T) runtime.Provider { return nil }, worker.LiveObservation{LastActivity: &oldEnough}, true},
		{"nil provider, activity too recent", func(*testing.T) runtime.Provider { return nil }, worker.LiveObservation{LastActivity: &tooRecent}, false},
		{"no activity, idle wait succeeds", func(t *testing.T) runtime.Provider {
			fake := started(t)
			fake.WaitForIdleErrors["sess-worker"] = nil
			return fake
		}, worker.LiveObservation{}, true},
		{"no activity, idle wait fails", func(t *testing.T) runtime.Provider {
			fake := started(t)
			fake.WaitForIdleErrors["sess-worker"] = errors.New("timed out waiting for idle")
			return fake
		}, worker.LiveObservation{}, false},
		{"activityless timed-only sleeper", func(t *testing.T) runtime.Provider {
			fake := &activitylessTimedOnlyNudgeProvider{Fake: started(t)}
			fake.WaitForIdleErrors["sess-worker"] = errors.New("idle wait should not be required")
			return fake
		}, worker.LiveObservation{}, true},
		{"auto over a backend without the capability", func(t *testing.T) runtime.Provider {
			return auto.New(started(t), runtime.NewFake())
		}, worker.LiveObservation{LastActivity: &oldEnough}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := pollerSessionIdleEnough(target, tc.sp(t), 3*time.Second, tc.obs)
			verdict := pollerDeliveryGate(target, tc.sp(t), 3*time.Second, tc.obs)
			if old != tc.want {
				t.Fatalf("pollerSessionIdleEnough = %v, want %v (the pre-readiness expectation)", old, tc.want)
			}
			if verdict.deliver != old {
				t.Fatalf("gate deliver = %v, the old gate says %v", verdict.deliver, old)
			}
			if !verdict.deliver && verdict.reason != pollerHoldNotQuiescent {
				t.Fatalf("hold reason = %q, want %q for a provider without readiness", verdict.reason, pollerHoldNotQuiescent)
			}
		})
	}
	if v := pollerDeliveryGate(target, nil, 0, worker.LiveObservation{}); !v.deliver {
		t.Fatalf("gate with quiescence 0 = %+v, want deliver: the caller asked for no gate", v)
	}
}
