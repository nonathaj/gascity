package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// staleLiveListStore serves a stale pre-close row from List while every other
// operation sees the real, closed row.
//
// This is the shape that defeats the dead-assignee path today. The release
// decision is made entirely from reads (the assigned-work snapshot, then
// liveWorkAssignmentStillReleasable's live List), but the release WRITE is
// unconditional on status. Any read that lags the close — a stale snapshot, a
// close landing between the liveness read and the write — reopens a genuinely
// closed row, because closed-ness is never an input to the decision.
type staleLiveListStore struct {
	beads.Store
	stale beads.Bead
}

func (s *staleLiveListStore) List(query beads.ListQuery) ([]beads.Bead, error) {
	if query.Status == s.stale.Status {
		return []beads.Bead{s.stale}, nil
	}
	return s.Store.List(query)
}

// closedRowFixture builds a pool-routed work bead whose assignee names a
// session that no longer exists, closed in the store with an audit
// close_reason, plus the pre-close snapshot the reconciler is still holding.
func closedRowFixture(t *testing.T, snapshotStatus string) (*beads.MemStore, beads.Bead) {
	t.Helper()
	store := beads.NewMemStore()
	work, err := store.Create(beads.Bead{
		Title:    "routed work closed while its session was dead",
		Type:     "task",
		Status:   snapshotStatus,
		Assignee: "rig--pool__coder-gc-session-deadbeef",
		Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "worker"},
	})
	if err != nil {
		t.Fatalf("create work bead: %v", err)
	}

	// The snapshot the reconciler carries, taken while the row was still live.
	snapshot, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("snapshot work bead: %v", err)
	}

	// The row is then closed with an audit reason.
	if err := store.Update(work.ID, beads.UpdateOpts{
		Metadata: map[string]string{"close_reason": "superseded by gcty-h83w"},
	}); err != nil {
		t.Fatalf("stamp close_reason: %v", err)
	}
	if err := store.Close(work.ID); err != nil {
		t.Fatalf("close work bead: %v", err)
	}
	closed, err := store.Get(work.ID)
	if err != nil {
		t.Fatalf("reload closed work bead: %v", err)
	}
	if closed.Status != "closed" {
		t.Fatalf("fixture status = %q, want closed", closed.Status)
	}
	return store, snapshot
}

func closedRowCity() *config.City {
	return &config.City{Agents: []config.Agent{{
		Name:              "worker",
		MinActiveSessions: intPtr(0),
		MaxActiveSessions: intPtr(2),
	}}}
}

// TestReleaseOrphanedPoolAssignments_RefusesClosedRow pins REQ-004 / AC-3: the
// dead-assignee path must never move a closed row back to open, even when every
// liveness read it consults still reports the row as live.
//
// The gate is closed-ness, not close-reason presence. close_reason is not a
// readable field on every close path (BdStore.CloseWithReason forwards the
// reason to `bd close --reason` without persisting it), so a predicate keyed on
// it fails open — which is the failure mode under repair. Closed-ness is
// readable and strictly stronger: every row REQ-004 names is closed.
//
// The open-status case is the one that reaches the unconditional write.
// releasePoolAssignmentIfCurrent declines any snapshot that is not in_progress
// (the issue #2793 open-strand path), so the release falls through to
// releasePoolAssignmentWithRecheck, whose Update forces status=open with no
// closed-ness condition at all.
func TestReleaseOrphanedPoolAssignments_RefusesClosedRow(t *testing.T) {
	for _, snapshotStatus := range []string{"open", "in_progress"} {
		t.Run("stale_liveness_read_"+snapshotStatus, func(t *testing.T) {
			mem, snapshot := closedRowFixture(t, snapshotStatus)
			store := &staleLiveListStore{Store: mem, stale: snapshot}

			// No open sessions: the assignee is genuinely dead.
			released := releaseOrphanedPoolAssignmentsFromBeads(
				store, closedRowCity(), "", nil,
				[]beads.Bead{snapshot}, nil, nil, nil,
			)
			if len(released) != 0 {
				t.Errorf("released = %v, want none — a closed row must not be reopened", released)
			}

			got, err := mem.Get(snapshot.ID)
			if err != nil {
				t.Fatalf("reload work bead: %v", err)
			}
			if got.Status != "closed" {
				t.Errorf("status = %q, want closed — the closed row was reopened", got.Status)
			}
			if got.Assignee != snapshot.Assignee {
				t.Errorf("assignee = %q, want %q — a closed row must not be mutated", got.Assignee, snapshot.Assignee)
			}
		})
	}
}

// TestReleaseOrphanedPoolAssignments_ClosedRowEmitsNoReopenEvent is the literal
// AC-3 assertion: no bead.dead_assignee_reopened is emitted for a
// closed-with-reason bead. It runs the release and the emitter together, the
// way city_runtime.go does, so the absence is proven end to end rather than
// from the release return value alone.
func TestReleaseOrphanedPoolAssignments_ClosedRowEmitsNoReopenEvent(t *testing.T) {
	mem, snapshot := closedRowFixture(t, "open")
	store := &staleLiveListStore{Store: mem, stale: snapshot}
	assigned := []beads.Bead{snapshot}

	released := releaseOrphanedPoolAssignmentsFromBeads(
		store, closedRowCity(), "", nil, assigned, nil, nil, nil,
	)

	rec := &capturingRecorder{}
	emitDeadAssigneeReopenedEvents(rec, assigned, released, time.Now())

	if len(rec.events) != 0 {
		t.Fatalf("emitted %d event(s) for a closed row, want 0: %+v", len(rec.events), rec.events)
	}
}

// TestReleaseOrphanedPoolAssignments_StillReleasesLiveOrphan guards the
// widening: refusing closed rows must not stop the path from doing its job on a
// genuinely live orphan.
func TestReleaseOrphanedPoolAssignments_StillReleasesLiveOrphan(t *testing.T) {
	for _, status := range []string{"open", "in_progress"} {
		t.Run(status, func(t *testing.T) {
			store := beads.NewMemStore()
			work, err := store.Create(beads.Bead{
				Title:    "live routed work orphaned by a dead session",
				Type:     "task",
				Status:   status,
				Assignee: "rig--pool__coder-gc-session-deadbeef",
				Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "worker"},
			})
			if err != nil {
				t.Fatalf("create work bead: %v", err)
			}

			released := releaseOrphanedPoolAssignmentsFromBeads(
				store, closedRowCity(), "", nil,
				[]beads.Bead{work}, nil, nil, nil,
			)
			if len(released) != 1 || released[0].ID != work.ID {
				t.Fatalf("released = %v, want [%s] — the live orphan must still be released", released, work.ID)
			}

			got, err := store.Get(work.ID)
			if err != nil {
				t.Fatalf("reload work bead: %v", err)
			}
			if got.Status != "open" {
				t.Errorf("status = %q, want open", got.Status)
			}
			if got.Assignee != "" {
				t.Errorf("assignee = %q, want empty after release", got.Assignee)
			}
		})
	}
}
