package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

var errScriptedWatcherDone = errors.New("scripted watcher exhausted")

// scriptedWatcher yields a fixed event sequence, then errScriptedWatcherDone.
type scriptedWatcher struct {
	events []events.Event
	closed bool
}

func (w *scriptedWatcher) Next() (events.Event, error) {
	if len(w.events) == 0 {
		return events.Event{}, errScriptedWatcherDone
	}
	evt := w.events[0]
	w.events = w.events[1:]
	return evt, nil
}

func (w *scriptedWatcher) Close() error {
	w.closed = true
	return nil
}

func TestLedgerWakeWatcherPassesOnlyItsLedgersBeadEvents(t *testing.T) {
	inner := &scriptedWatcher{events: []events.Event{
		{Seq: 1, Type: events.BeadUpdated, Subject: "fe-926306"},         // city store: a session heartbeat
		{Seq: 2, Type: events.SessionWoke, Subject: "fe-926306"},         // not a bead event: the wait loop judges it
		{Seq: 3, Type: events.BeadClosed, Subject: "gcty-iazh"},          // own ledger
		{Seq: 4, Type: events.BeadCreated, Subject: "gf-3zgjb"},          // another rig's store
		{Seq: 5, Type: events.BeadUpdated, Subject: ""},                  // unattributable: fail open
		{Seq: 6, Type: events.BeadCreated, Subject: "gcty-wisp-2ifhusy"}, // own ledger, ephemeral
	}}
	w := newLedgerWakeWatcher(inner, "gcty")

	var got []uint64
	for {
		evt, err := w.Next()
		if err != nil {
			if !errors.Is(err, errScriptedWatcherDone) {
				t.Fatalf("Next error = %v, want the inner watcher's own terminal error passed through", err)
			}
			break
		}
		got = append(got, evt.Seq)
	}
	if want := []uint64{2, 3, 5, 6}; !slices.Equal(got, want) {
		t.Fatalf("delivered event seqs = %v, want %v: only bead events attributed to another ledger may be dropped", got, want)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !inner.closed {
		t.Fatal("Close did not reach the inner watcher")
	}
}

func TestNewLedgerWakeWatcherWithoutPrefixIsTheInnerWatcher(t *testing.T) {
	inner := &scriptedWatcher{}
	if got := newLedgerWakeWatcher(inner, ""); got != events.Watcher(inner) {
		t.Fatalf("newLedgerWakeWatcher(inner, \"\") = %T, want the inner watcher unwrapped: an unknown ledger must not narrow the wake", got)
	}
}

// writeScopeIssuePrefix gives dir a bd config naming its issue prefix, the
// first source issuePrefixForScope consults.
func writeScopeIssuePrefix(t *testing.T, dir, prefix string) {
	t.Helper()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", beadsDir, err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "config.yaml"), []byte("issue_prefix: "+prefix+"\n"), 0o644); err != nil {
		t.Fatalf("write bd config: %v", err)
	}
}

func TestWorkflowServeWakeLedgerPrefix(t *testing.T) {
	agentCfg := config.Agent{Name: config.ControlDispatcherAgentName, Dir: "fixture"}
	controlQuery := workflowServeControlReadyQuery(agentCfg)

	newCity := func(t *testing.T) (cityPath, rigPath string) {
		t.Helper()
		configureIsolatedRuntimeEnv(t)
		cityPath = t.TempDir()
		rigPath = filepath.Join(cityPath, "rigs", "fixture")
		writeScopeIssuePrefix(t, cityPath, "fx")
		writeScopeIssuePrefix(t, rigPath, "fixp")
		return cityPath, rigPath
	}

	t.Run("single-ledger rig scope narrows to its issue prefix", func(t *testing.T) {
		cityPath, rigPath := newCity(t)
		seedCLIStorageRoutes(t, cityPath, nil)
		if got := workflowServeWakeLedgerPrefix(cityPath, rigPath, controlQuery, nil); got != "fixp" {
			t.Fatalf("wake ledger prefix = %q, want %q", got, "fixp")
		}
	})

	t.Run("single-ledger city scope narrows to its issue prefix", func(t *testing.T) {
		cityPath, _ := newCity(t)
		seedCLIStorageRoutes(t, cityPath, nil)
		if got := workflowServeWakeLedgerPrefix(cityPath, cityPath, controlQuery, nil); got != "fx" {
			t.Fatalf("wake ledger prefix = %q, want %q", got, "fx")
		}
	})

	t.Run("custom work query is not narrowed", func(t *testing.T) {
		cityPath, rigPath := newCity(t)
		seedCLIStorageRoutes(t, cityPath, nil)
		if got := workflowServeWakeLedgerPrefix(cityPath, rigPath, "bd ready --json --limit=1", nil); got != "" {
			t.Fatalf("wake ledger prefix = %q, want \"\": a custom work_query may read any ledger", got)
		}
	})

	t.Run("relocated city scope is not narrowed", func(t *testing.T) {
		cityPath, _ := newCity(t)
		seedCLIStorageRoutes(t, cityPath, messagingSplitRoutes(beads.NewMemStore()))
		if got := workflowServeWakeLedgerPrefix(cityPath, cityPath, controlQuery, nil); got != "" {
			t.Fatalf("wake ledger prefix = %q, want \"\": the scan reads the graph binding, whose ids the scope prefix does not name", got)
		}
	})

	t.Run("federated rig scope is not narrowed", func(t *testing.T) {
		cityPath, rigPath := newCity(t)
		seedCLIStorageRoutes(t, cityPath, messagingSplitRoutes(beads.NewMemStore()))
		if got := workflowServeWakeLedgerPrefix(cityPath, rigPath, controlQuery, nil); got != "" {
			t.Fatalf("wake ledger prefix = %q, want \"\": the scan also reads the city graph binding", got)
		}
	})

	t.Run("scope with no known prefix is not narrowed", func(t *testing.T) {
		configureIsolatedRuntimeEnv(t)
		cityPath := t.TempDir()
		rigPath := filepath.Join(cityPath, "rigs", "fixture")
		seedCLIStorageRoutes(t, cityPath, nil)
		if got := workflowServeWakeLedgerPrefix(cityPath, rigPath, controlQuery, nil); got != "" {
			t.Fatalf("wake ledger prefix = %q, want \"\"", got)
		}
	})
}

// TestRunWorkflowServeFollowWakesOnlyOnItsLedgersBeadEvents pins the wiring:
// the follow loop's wake stream is narrowed to the ledger its scan reads, so a
// bead event from another store never reaches the wait at all (gcty-fz6r).
func TestRunWorkflowServeFollowWakesOnlyOnItsLedgersBeadEvents(t *testing.T) {
	fake := events.NewFake()

	prevList := workflowServeList
	prevControl := controlDispatcherServe
	prevProvider := workflowServeOpenEventsProvider
	prevWait := workflowServeWaitForWake
	t.Cleanup(func() {
		workflowServeList = prevList
		controlDispatcherServe = prevControl
		workflowServeOpenEventsProvider = prevProvider
		workflowServeWaitForWake = prevWait
	})

	workflowServeOpenEventsProvider = func(io.Writer) (events.Provider, error) {
		return fake, nil
	}
	listCalls := 0
	workflowServeList = func(_, _ string, _ map[string]string) ([]hookBead, error) {
		listCalls++
		if listCalls == 1 {
			// The watcher is open before the first drain, so both are delivered.
			fake.Record(events.Event{Type: events.BeadUpdated, Actor: "cache-reconcile", Subject: "fe-926306"})
			fake.Record(events.Event{Type: events.BeadClosed, Actor: "cache-reconcile", Subject: "gcty-iazh"})
		}
		return nil, nil
	}
	controlDispatcherServe = func(_, _, beadID string, _ io.Writer, _ io.Writer) error {
		t.Fatalf("controlDispatcherServe(%s) called with an empty queue", beadID)
		return nil
	}
	stopErr := errors.New("stop after the first delivered event")
	var delivered []events.Event
	workflowServeWaitForWake = func(eventCh <-chan workflowWatchResult, _ time.Duration, _ int) (bool, error) {
		res := <-eventCh
		if res.err != nil {
			t.Fatalf("watch error before any event: %v", res.err)
		}
		delivered = append(delivered, res.evt)
		return false, stopErr
	}

	agent := config.Agent{Name: "control-dispatcher"}
	err := runWorkflowServeFollow(agent, t.TempDir(), t.TempDir(), agent.EffectiveWorkQuery(), nil, "gcty", io.Discard)
	if !errors.Is(err, stopErr) {
		t.Fatalf("runWorkflowServeFollow error = %v, want %v", err, stopErr)
	}
	if len(delivered) != 1 || delivered[0].Subject != "gcty-iazh" {
		t.Fatalf("first event reaching the wait = %+v, want gcty-iazh: the city store's bead event must be dropped before the wait", delivered)
	}
}
