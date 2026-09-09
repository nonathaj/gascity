package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

// flappingWorkFixture builds pool-routed work assigned to a session identity
// that resolves to no open session bead — the dead-assignee shape the orphan
// sweep reopens. The store holds no session bead at all, so every sweep sees a
// confirmed-dead assignee.
func flappingWorkFixture(t *testing.T) (*beads.MemStore, beads.Bead) {
	t.Helper()
	store := beads.NewMemStore()
	work, err := store.Create(beads.Bead{
		Title:    "routed work whose worker keeps dying",
		Type:     "task",
		Assignee: "worker-mc-dead",
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "worker"},
	})
	if err != nil {
		t.Fatalf("create work bead: %v", err)
	}
	inProgress := "in_progress"
	if err := store.Update(work.ID, beads.UpdateOpts{Status: &inProgress}); err != nil {
		t.Fatalf("set work in_progress: %v", err)
	}
	work, err = store.Get(work.ID)
	if err != nil {
		t.Fatalf("reload work bead: %v", err)
	}
	return store, work
}

// reclaimByDeadSession re-assigns the reopened bead to a dead session and puts
// it back in_progress — one flap cycle. This is what makes the same bead
// eligible for the orphan sweep again on the next patrol tick; without it a
// reopened bead sits open+unassigned and is skipped.
func reclaimByDeadSession(t *testing.T, store *beads.MemStore, id string) beads.Bead {
	t.Helper()
	assignee := "worker-mc-dead"
	inProgress := "in_progress"
	if err := store.Update(id, beads.UpdateOpts{Assignee: &assignee, Status: &inProgress}); err != nil {
		t.Fatalf("re-claim work bead: %v", err)
	}
	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("reload re-claimed work bead: %v", err)
	}
	return got
}

// sweepOnce runs one orphan-release patrol tick over the given work snapshot,
// spending from budget — the caller holds it across ticks the way the
// controller does.
func sweepOnce(store beads.Store, work beads.Bead, budget *reopenBudget) []releasedPoolAssignment {
	return releaseOrphanedPoolAssignments(
		store,
		&config.City{Agents: []config.Agent{{Name: "worker", MinActiveSessions: intPtr(0), MaxActiveSessions: intPtr(2)}}},
		"",
		nil, // no open session beads: the assignee is confirmed dead
		[]beads.Bead{work},
		nil, nil, nil,
		budget,
	)
}

// REQ-005: reopen activity must not degrade quietly. The same bead flapping
// through the dead-assignee path is reopened at most reopenBudgetLimit times
// inside one patrol window; past the budget the sweep refuses to reopen it
// again, so 73 silent reopens over 5 hours is structurally impossible.
func TestReleaseOrphanedPoolAssignments_BoundsReopensPerBead(t *testing.T) {
	budget := newReopenBudget()
	store, work := flappingWorkFixture(t)

	for attempt := 1; attempt <= reopenBudgetLimit; attempt++ {
		released := sweepOnce(store, work, budget)
		if len(released) != 1 {
			t.Fatalf("attempt %d: released %d beads, want 1 (inside budget)", attempt, len(released))
		}
		if released[0].ReopenCount != attempt {
			t.Fatalf("attempt %d: ReopenCount = %d, want %d", attempt, released[0].ReopenCount, attempt)
		}
		wantExhausted := attempt == reopenBudgetLimit
		if released[0].BudgetExhausted != wantExhausted {
			t.Fatalf("attempt %d: BudgetExhausted = %v, want %v", attempt, released[0].BudgetExhausted, wantExhausted)
		}
		got, _ := store.Get(work.ID)
		if got.Status != "open" || got.Assignee != "" {
			t.Fatalf("attempt %d: work = status %q assignee %q, want open/unassigned", attempt, got.Status, got.Assignee)
		}
		work = reclaimByDeadSession(t, store, work.ID)
	}

	// Budget spent: the next flap must NOT be reopened.
	released := sweepOnce(store, work, budget)
	if len(released) != 0 {
		t.Fatalf("past budget: released %d beads, want 0", len(released))
	}
	got, _ := store.Get(work.ID)
	if got.Status != "in_progress" || got.Assignee != "worker-mc-dead" {
		t.Fatalf("past budget: work = status %q assignee %q, want the untouched in_progress/worker-mc-dead row", got.Status, got.Assignee)
	}
}

// The budget is per bead: one bead exhausting its budget must never starve a
// different bead's first legitimate reopen.
func TestReleaseOrphanedPoolAssignments_BudgetIsPerBead(t *testing.T) {
	budget := newReopenBudget()
	store, hot := flappingWorkFixture(t)
	for attempt := 1; attempt <= reopenBudgetLimit; attempt++ {
		if released := sweepOnce(store, hot, budget); len(released) != 1 {
			t.Fatalf("warming attempt %d: released %d, want 1", attempt, len(released))
		}
		hot = reclaimByDeadSession(t, store, hot.ID)
	}
	if released := sweepOnce(store, hot, budget); len(released) != 0 {
		t.Fatalf("hot bead should be over budget, got %d releases", len(released))
	}

	cold, err := store.Create(beads.Bead{
		Title:    "a different bead, first strand",
		Type:     "task",
		Assignee: "worker-mc-dead",
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "worker"},
	})
	if err != nil {
		t.Fatalf("create second work bead: %v", err)
	}
	inProgress := "in_progress"
	if err := store.Update(cold.ID, beads.UpdateOpts{Status: &inProgress}); err != nil {
		t.Fatalf("set second work in_progress: %v", err)
	}
	cold, _ = store.Get(cold.ID)

	released := sweepOnce(store, cold, budget)
	if len(released) != 1 || released[0].ReopenCount != 1 {
		t.Fatalf("cold bead released = %+v, want one release at count 1", released)
	}
}

// The window is rolling per bead: once it elapses the budget re-arms, so a
// bead that flapped hours ago is not permanently refused.
func TestReopenBudget_WindowExpiryRearmsBudget(t *testing.T) {
	budget := newReopenBudget()
	start := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= reopenBudgetLimit; i++ {
		if !budget.allows(reopenBudgetKey("", "w-1"), start) {
			t.Fatalf("reopen %d must be inside the budget", i)
		}
		if got := budget.note(reopenBudgetKey("", "w-1"), start); got != i {
			t.Fatalf("noteBeadReopen = %d, want %d", got, i)
		}
	}
	if budget.allows(reopenBudgetKey("", "w-1"), start.Add(reopenBudgetWindow-time.Second)) {
		t.Fatalf("budget must stay spent for the rest of the window")
	}

	after := start.Add(reopenBudgetWindow)
	if !budget.allows(reopenBudgetKey("", "w-1"), after) {
		t.Fatalf("budget must re-arm once the window elapses")
	}
	if got := budget.note(reopenBudgetKey("", "w-1"), after); got != 1 {
		t.Fatalf("post-window count = %d, want a fresh window at 1", got)
	}
}

// Bead IDs are unique only within a store — two rigs can each hold a "gc-1"
// (see TestReleaseOrphanedPoolAssignments_ReopensCrossStoreIDCollisions). The
// budget key carries the owning store ref so one rig's flapping bead cannot
// spend an unrelated bead's budget in another.
func TestReopenBudget_SeparatesCollidingIDsAcrossStores(t *testing.T) {
	budget := newReopenBudget()
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	alpha := reopenBudgetKey("rig:alpha", "gc-1")
	beta := reopenBudgetKey("rig:beta", "gc-1")
	if alpha == beta {
		t.Fatalf("same-ID beads in different stores must not share a budget key")
	}

	for i := 0; i < reopenBudgetLimit; i++ {
		budget.note(alpha, now)
	}
	if budget.allows(alpha, now) {
		t.Fatalf("rig:alpha gc-1 should be over budget")
	}
	if !budget.allows(beta, now) {
		t.Fatalf("rig:beta gc-1 must still be fully funded")
	}
}

// emitReopenBudgetExhaustedEvents publishes the alarm exactly once, on the
// release that spends the budget, carrying the machine-readable budget facts.
func TestEmitReopenBudgetExhaustedEvents_EmitsAlarmPayload(t *testing.T) {
	assigned := []beads.Bead{
		{ID: "w-1", Assignee: "worker-mc-dead", Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "worker"}},
		{ID: "w-2", Assignee: "worker-mc-other"},
	}
	released := []releasedPoolAssignment{
		{ID: "w-1", Index: 0, ReopenCount: reopenBudgetLimit, BudgetExhausted: true},
		{ID: "w-2", Index: 1, ReopenCount: 1},
	}
	rec := &capturingRecorder{}
	now := time.Date(2026, 5, 23, 12, 0, 0, 0, time.UTC)

	emitReopenBudgetExhaustedEvents(rec, assigned, released, now)

	if len(rec.events) != 1 {
		t.Fatalf("event count = %d, want 1 (only the exhausted bead alarms)", len(rec.events))
	}
	e := rec.events[0]
	if e.Type != events.BeadReopenBudgetExhausted {
		t.Fatalf("type = %q, want %q", e.Type, events.BeadReopenBudgetExhausted)
	}
	if e.Subject != "w-1" {
		t.Fatalf("subject = %q, want w-1", e.Subject)
	}
	var p api.BeadReopenBudgetExhaustedPayload
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.BeadID != "w-1" || p.DeadAssignee != "worker-mc-dead" || p.RoutedTo != "worker" {
		t.Fatalf("payload identity = %+v, want bead_id=w-1 dead_assignee=worker-mc-dead routed_to=worker", p)
	}
	if p.ReopenCount != reopenBudgetLimit || p.Limit != reopenBudgetLimit {
		t.Fatalf("payload budget = count %d limit %d, want %d/%d", p.ReopenCount, p.Limit, reopenBudgetLimit, reopenBudgetLimit)
	}
	if p.WindowSeconds != int(reopenBudgetWindow.Seconds()) {
		t.Fatalf("payload window_seconds = %d, want %d", p.WindowSeconds, int(reopenBudgetWindow.Seconds()))
	}
}

// A nil recorder, an empty release list, or releases that never spent their
// budget are all no-ops — the alarm is reserved for actual exhaustion.
func TestEmitReopenBudgetExhaustedEvents_NoOpWithoutExhaustion(t *testing.T) {
	emitReopenBudgetExhaustedEvents(nil, nil, []releasedPoolAssignment{{ID: "x", BudgetExhausted: true}}, time.Now())

	rec := &capturingRecorder{}
	emitReopenBudgetExhaustedEvents(rec, nil, nil, time.Now())
	emitReopenBudgetExhaustedEvents(rec,
		[]beads.Bead{{ID: "w-1"}},
		[]releasedPoolAssignment{{ID: "w-1", Index: 0, ReopenCount: 1}},
		time.Now(),
	)
	if len(rec.events) != 0 {
		t.Fatalf("expected no alarm events, got %d", len(rec.events))
	}
}
