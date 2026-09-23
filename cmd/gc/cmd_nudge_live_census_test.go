// Fork-local pins: the Leg B liveness census, run against persisted session
// beads. Offered upstream.

package main

import (
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// persistedSessionFenceStates lists each session state the lifecycle persists
// and whether the production census counts a session stored in that state as
// still holding its nudge fence. Nudges fenced to a session that holds its
// fence stay pending; the others rebind to a successor (Leg B).
var persistedSessionFenceStates = []struct {
	state      string
	holdsFence bool
}{
	{state: "active", holdsFence: true},
	{state: "awake", holdsFence: true},
	{state: "asleep", holdsFence: true},
	{state: "suspended", holdsFence: true},
	{state: "draining", holdsFence: true},
	{state: "quarantined", holdsFence: true},
	{state: "creating", holdsFence: true},
	{state: "start-pending", holdsFence: true},
	{state: "", holdsFence: true}, // legacy bead written before state metadata
	// The session projection maps drained to asleep (normalizeInfoState in
	// internal/session), so the StateDrained case in sessionHoldsNudgeFence is
	// unreachable and a drained session keeps its fence. Inherited from
	// upstream #5775.
	{state: "drained", holdsFence: true},
	{state: "archived", holdsFence: false},
	{state: "failed-create", holdsFence: false},
}

// closedSessionFixture keys the fixture's closed session bead. It stores an
// active state, but a closed session bead never holds a fence.
const closedSessionFixture = "<closed bead>"

// newLiveNudgeFenceCensusCity builds a file-backed city whose store holds one
// open session bead per persistedSessionFenceStates entry, plus one closed
// session bead. It returns the city path and each bead's ID keyed by its stored
// state, or by closedSessionFixture for the closed bead.
func newLiveNudgeFenceCensusCity(t *testing.T) (string, map[string]string) {
	t.Helper()
	t.Setenv("GC_BEADS", "file")
	cityDir := t.TempDir()
	writeMinimalCityToml(t, cityDir)
	store, err := openCityStoreAt(cityDir)
	if err != nil {
		t.Fatalf("openCityStoreAt: %v", err)
	}
	createSession := func(name, state string) string {
		t.Helper()
		b, err := store.Create(beads.Bead{
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel},
			Metadata: map[string]string{
				"session_name": name,
				"state":        state,
				"template":     "worker",
				"agent_name":   "worker",
			},
		})
		if err != nil {
			t.Fatalf("creating session bead %q: %v", name, err)
		}
		return b.ID
	}
	ids := make(map[string]string, len(persistedSessionFenceStates)+1)
	for _, tt := range persistedSessionFenceStates {
		name := "s-" + tt.state
		if tt.state == "" {
			name = "s-legacy"
		}
		ids[tt.state] = createSession(name, tt.state)
	}
	closedID := createSession("s-closed", "active")
	if err := store.Close(closedID); err != nil {
		t.Fatalf("closing session bead %s: %v", closedID, err)
	}
	ids[closedSessionFixture] = closedID
	if err := closeBeadStoreHandle(store); err != nil {
		t.Fatalf("closeBeadStoreHandle: %v", err)
	}
	return cityDir, ids
}

// requireProductionNudgeFenceCensus fails the test when the census seam is
// set, so the test runs loadLiveNudgeFenceSessionIDsFromCity itself.
func requireProductionNudgeFenceCensus(t *testing.T) {
	t.Helper()
	if liveNudgeFenceSessionIDs != nil {
		t.Fatal("liveNudgeFenceSessionIDs seam is set; this test must run the production census")
	}
}

// fencedSessionStates returns the sorted stored states of the sessions items
// are fenced to, naming any fence stateOf does not know.
func fencedSessionStates(items []queuedNudge, stateOf map[string]string) []string {
	states := make([]string, 0, len(items))
	for _, item := range items {
		state, ok := stateOf[item.SessionID]
		if !ok {
			state = "unknown session " + item.SessionID
		}
		states = append(states, state)
	}
	sort.Strings(states)
	return states
}

// TestLiveNudgeFenceCensus_PersistedSessionStates pins the production census
// against persisted session beads: every session that keeps its seat is
// listed, drained included, and archived, failed-create and closed sessions
// are not.
func TestLiveNudgeFenceCensus_PersistedSessionStates(t *testing.T) {
	requireProductionNudgeFenceCensus(t)
	cityDir, ids := newLiveNudgeFenceCensusCity(t)

	live := loadLiveNudgeFenceSessionIDsFromCity(cityDir)
	if live == nil {
		t.Fatal("census = nil (unavailable), want the persisted sessions")
	}
	wantLive := 0
	for _, tt := range persistedSessionFenceStates {
		if tt.holdsFence {
			wantLive++
		}
		if _, listed := live[ids[tt.state]]; listed != tt.holdsFence {
			t.Errorf("stored state %q: in census = %v, want %v", tt.state, listed, tt.holdsFence)
		}
	}
	if _, listed := live[ids[closedSessionFixture]]; listed {
		t.Error("closed session bead: in census = true, want false")
	}
	if len(live) != wantLive {
		t.Errorf("census lists %d sessions, want %d", len(live), wantLive)
	}
}

// TestClaimDueQueuedNudgesForTarget_RealCensusRebindsOnlySupersededPredecessors
// drives Leg B through the production census. A successor claims the nudges
// fenced to archived, failed-create and closed predecessors and re-fences them
// onto itself. Every nudge fenced to a predecessor that keeps its seat, drained
// included, stays pending, and nothing is dead-lettered.
func TestClaimDueQueuedNudgesForTarget_RealCensusRebindsOnlySupersededPredecessors(t *testing.T) {
	requireProductionNudgeFenceCensus(t)
	cityDir, ids := newLiveNudgeFenceCensusCity(t)
	successor := ids["active"]

	stateOf := make(map[string]string, len(ids))
	wantClaimed := []string{closedSessionFixture}
	var wantPending []string
	for _, tt := range persistedSessionFenceStates {
		if ids[tt.state] == successor {
			continue
		}
		if tt.holdsFence {
			wantPending = append(wantPending, tt.state)
		} else {
			wantClaimed = append(wantClaimed, tt.state)
		}
	}
	sort.Strings(wantClaimed)
	sort.Strings(wantPending)

	now := time.Now().Add(-time.Minute)
	for state, id := range ids {
		if id == successor {
			continue
		}
		stateOf[id] = state
		item := newQueuedNudgeWithOptions("worker", "fenced to "+state, "session", now, queuedNudgeOptions{
			ID:                "n-" + id,
			SessionID:         id,
			ContinuationEpoch: "1",
		})
		if err := enqueueQueuedNudge(cityDir, item); err != nil {
			t.Fatalf("enqueueQueuedNudge(fenced to %q): %v", state, err)
		}
	}

	target := nudgeTarget{
		cityPath:          cityDir,
		agent:             config.Agent{Name: "worker"},
		sessionID:         successor,
		continuationEpoch: "2",
	}
	claimed, err := claimDueQueuedNudgesForTarget(cityDir, target, time.Now())
	if err != nil {
		t.Fatalf("claimDueQueuedNudgesForTarget: %v", err)
	}
	if got := fencedSessionStates(claimed, stateOf); !slices.Equal(got, wantClaimed) {
		t.Fatalf("claimed nudges fenced to %q, want %q", got, wantClaimed)
	}

	deliverable, rejected := splitQueuedNudgesForTarget(target, claimed)
	if len(rejected) != 0 {
		t.Fatalf("split rejected %v, want every claimed nudge re-fenced (not dead-lettered)", queuedNudgeIDs(rejected))
	}
	if len(deliverable) != len(wantClaimed) {
		t.Fatalf("split delivers %v, want %d nudges", queuedNudgeIDs(deliverable), len(wantClaimed))
	}
	for _, item := range deliverable {
		if item.SessionID != successor || item.ContinuationEpoch != "2" {
			t.Errorf("deliverable %s fenced to (%q, %q), want (%q, %q)", item.ID, item.SessionID, item.ContinuationEpoch, successor, "2")
		}
	}

	pending, inFlight, dead, err := listQueuedNudges(cityDir, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if got := fencedSessionStates(pending, stateOf); !slices.Equal(got, wantPending) {
		t.Errorf("pending nudges fenced to %q, want %q", got, wantPending)
	}
	if len(inFlight) != len(wantClaimed) || len(dead) != 0 {
		t.Errorf("in flight = %d, dead = %d; want %d and 0", len(inFlight), len(dead), len(wantClaimed))
	}
}

// TestClaimDueQueuedNudgesForTarget_UnavailableCensusFailsClosed pins the
// production census's unavailable result: when the city store will not open,
// the census is nil and a session-replacement nudge stays pending instead of
// being claimed or dead-lettered.
func TestClaimDueQueuedNudgesForTarget_UnavailableCensusFailsClosed(t *testing.T) {
	requireProductionNudgeFenceCensus(t)
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	item := newQueuedNudgeWithOptions("worker", "predecessor session", "session", time.Now().Add(-time.Minute), queuedNudgeOptions{
		ID:                "n-replaced",
		SessionID:         "gc-old",
		ContinuationEpoch: "1",
	})
	if err := enqueueQueuedNudge(dir, item); err != nil {
		t.Fatalf("enqueueQueuedNudge: %v", err)
	}

	// Every nudge helper tolerates the zero store a failed open yields.
	prev := openNudgeBeadStore
	t.Cleanup(func() { openNudgeBeadStore = prev })
	openNudgeBeadStore = func(string) beads.NudgesStore { return beads.NudgesStore{} }

	if live := loadLiveNudgeFenceSessionIDsFromCity(dir); live != nil {
		t.Fatalf("census = %v, want nil (unavailable)", live)
	}
	target := nudgeTarget{
		cityPath:          dir,
		agent:             config.Agent{Name: "worker"},
		sessionID:         "gc-new",
		continuationEpoch: "2",
	}
	claimed, err := claimDueQueuedNudgesForTarget(dir, target, time.Now())
	if err != nil {
		t.Fatalf("claimDueQueuedNudgesForTarget: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed = %v, want nothing while the census is unavailable", queuedNudgeIDs(claimed))
	}
	pending, _, dead, err := listQueuedNudges(dir, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(pending) != 1 || len(dead) != 0 {
		t.Fatalf("pending = %d, dead = %d; want 1 and 0", len(pending), len(dead))
	}
}

// TestClaimDueQueuedNudgesForTarget_PartialCensusFailsClosed pins the
// production census's partial result: when the session listing is partial, as
// bd list returns it when a row fails to parse, the census is unavailable. A
// nudge fenced to a live sibling whose row the listing dropped stays pending
// instead of rebinding to the successor.
func TestClaimDueQueuedNudgesForTarget_PartialCensusFailsClosed(t *testing.T) {
	requireProductionNudgeFenceCensus(t)
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	mem := beads.NewMemStore()
	successor, err := mem.Create(beads.Bead{
		Type:   sessionBeadType,
		Labels: []string{sessionBeadLabel},
		Metadata: map[string]string{
			"session_name": "s-successor",
			"state":        "active",
			"template":     "worker",
			"agent_name":   "worker",
		},
	})
	if err != nil {
		t.Fatalf("creating the successor's session bead: %v", err)
	}
	// gc-live-sibling has no bead: it stands for the row the partial listing
	// dropped.
	item := newQueuedNudgeWithOptions("worker", "live sibling session", "session", time.Now().Add(-time.Minute), queuedNudgeOptions{
		ID:                "n-sibling",
		SessionID:         "gc-live-sibling",
		ContinuationEpoch: "1",
	})
	if err := enqueueQueuedNudge(dir, item); err != nil {
		t.Fatalf("enqueueQueuedNudge: %v", err)
	}

	prev := openNudgeBeadStore
	t.Cleanup(func() { openNudgeBeadStore = prev })
	openNudgeBeadStore = func(string) beads.NudgesStore {
		return beads.NudgesStore{Store: &partialSessionListStore{MemStore: mem}}
	}

	// Errorf, so that a non-nil census still reaches the claim below and
	// reports what the successor takes.
	if live := loadLiveNudgeFenceSessionIDsFromCity(dir); live != nil {
		t.Errorf("census = %v, want nil (a partial listing is unavailable)", live)
	}
	target := nudgeTarget{
		cityPath:          dir,
		agent:             config.Agent{Name: "worker"},
		sessionID:         successor.ID,
		continuationEpoch: "2",
	}
	claimed, err := claimDueQueuedNudgesForTarget(dir, target, time.Now())
	if err != nil {
		t.Fatalf("claimDueQueuedNudgesForTarget: %v", err)
	}
	if len(claimed) != 0 {
		t.Fatalf("claimed = %v, want nothing while the census is partial", queuedNudgeIDs(claimed))
	}
	pending, _, dead, err := listQueuedNudges(dir, "worker", time.Now())
	if err != nil {
		t.Fatalf("listQueuedNudges: %v", err)
	}
	if len(pending) != 1 || len(dead) != 0 {
		t.Fatalf("pending = %d, dead = %d; want 1 and 0", len(pending), len(dead))
	}
}

// TestClaimDueQueuedNudgesForTarget_CensusIsLazyAndOncePerPass pins the cost
// of the census on a resolved target: a claim pass whose items are unfenced or
// fenced to the target's own session (the idle tick) never builds it, and a
// pass with several session-replacement items builds it exactly once.
func TestClaimDueQueuedNudgesForTarget_CensusIsLazyAndOncePerPass(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	tests := []struct {
		name      string
		items     []queuedNudgeOptions
		wantCalls int
	}{
		{name: "empty queue", wantCalls: 0},
		{name: "idle tick", items: []queuedNudgeOptions{
			{ID: "n-unfenced"},
			{ID: "n-current", SessionID: "gc-new", ContinuationEpoch: "2"},
			{ID: "n-stale-epoch", SessionID: "gc-new", ContinuationEpoch: "1"},
		}, wantCalls: 0},
		{name: "session replacement", items: []queuedNudgeOptions{
			{ID: "n-old-1", SessionID: "gc-old-1", ContinuationEpoch: "1"},
			{ID: "n-old-2", SessionID: "gc-old-2", ContinuationEpoch: "1"},
		}, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			now := time.Now().Add(-time.Minute)
			var wantIDs []string
			for _, opts := range tt.items {
				if err := enqueueQueuedNudge(dir, newQueuedNudgeWithOptions("worker", opts.ID, "session", now, opts)); err != nil {
					t.Fatalf("enqueueQueuedNudge(%s): %v", opts.ID, err)
				}
				wantIDs = append(wantIDs, opts.ID)
			}
			sort.Strings(wantIDs)

			calls := 0
			prev := liveNudgeFenceSessionIDs
			t.Cleanup(func() { liveNudgeFenceSessionIDs = prev })
			liveNudgeFenceSessionIDs = func(string) map[string]struct{} {
				calls++
				return map[string]struct{}{"gc-new": {}}
			}

			target := nudgeTarget{
				cityPath:          dir,
				agent:             config.Agent{Name: "worker"},
				sessionID:         "gc-new",
				continuationEpoch: "2",
			}
			claimed, err := claimDueQueuedNudgesForTarget(dir, target, time.Now())
			if err != nil {
				t.Fatalf("claimDueQueuedNudgesForTarget: %v", err)
			}
			gotIDs := queuedNudgeIDs(claimed)
			sort.Strings(gotIDs)
			if !slices.Equal(gotIDs, wantIDs) {
				t.Fatalf("claimed = %v, want %v", gotIDs, wantIDs)
			}
			if calls != tt.wantCalls {
				t.Fatalf("census built %d times in one claim pass, want %d", calls, tt.wantCalls)
			}
		})
	}
}

// TestClaimDueQueuedNudgesForTarget_CensusClosesTheStoreItOpens pins that the
// Leg B census releases the bead store it opens. A claim pass that holds a
// session-replacement item builds the census, which opens its own store on
// every pass; like the other per-tick poll helpers
// (TestNudgePollHelpersCloseEveryStoreTheyOpen), it must close what it opens so
// sql-server connections do not accumulate across poll ticks while such an item
// waits.
func TestClaimDueQueuedNudgesForTarget_CensusClosesTheStoreItOpens(t *testing.T) {
	requireProductionNudgeFenceCensus(t)
	t.Setenv("GC_BEADS", "file")
	opens, closes := installCountingNudgeStoreSeam(t)
	dir := t.TempDir()
	item := newQueuedNudgeWithOptions("worker", "replaced session", "session", time.Now().Add(-time.Minute), queuedNudgeOptions{
		ID:                "n-replaced",
		SessionID:         "gc-old",
		ContinuationEpoch: "1",
	})
	if err := enqueueQueuedNudge(dir, item); err != nil {
		t.Fatalf("enqueueQueuedNudge: %v", err)
	}
	opensBeforeClaims := *opens
	target := nudgeTarget{
		cityPath:          dir,
		agent:             config.Agent{Name: "worker"},
		sessionID:         "gc-new",
		continuationEpoch: "2",
	}
	const passes = 3
	for i := 0; i < passes; i++ {
		if _, err := claimDueQueuedNudgesForTarget(dir, target, time.Now()); err != nil {
			t.Fatalf("claimDueQueuedNudgesForTarget: %v", err)
		}
	}
	// Each pass opens the claim pass's maintenance store and the census's own
	// store. If the census comes to reuse a store it did not open, this test no
	// longer exercises its close; move the pin with it.
	if got := *opens - opensBeforeClaims; got <= passes {
		t.Fatalf("%d claim passes opened %d stores; want the census to open its own store on each pass", passes, got)
	}
	if *closes != *opens {
		t.Fatalf("bead store leak: opens=%d closes=%d (the census must close the store it opens)", *opens, *closes)
	}
}
