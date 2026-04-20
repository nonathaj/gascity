package main

import (
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestBuildAwakeInputFromReconcilerUsesLifecycleProjectionForCompatibilityStates(t *testing.T) {
	now := time.Now().UTC()
	input := buildAwakeInputFromReconciler(
		&config.City{},
		[]beads.Bead{{
			ID:     "mc-session-1",
			Status: "open",
			Type:   "session",
			Metadata: map[string]string{
				"state":        "stopped",
				"session_name": "s-worker",
				"template":     "worker",
			},
		}},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		now,
	)

	if len(input.SessionBeads) != 1 {
		t.Fatalf("SessionBeads length = %d, want 1", len(input.SessionBeads))
	}
	if got := input.SessionBeads[0].State; got != "asleep" {
		t.Fatalf("State = %q, want asleep-compatible projection for stopped", got)
	}
}

func TestBuildAwakeInputFromReconcilerPopulatesPendingInteractions(t *testing.T) {
	now := time.Now().UTC()
	sp := runtime.NewFake()
	sp.SetPendingInteraction("s-worker", &runtime.PendingInteraction{
		RequestID: "req-1",
		Kind:      "question",
		Prompt:    "approve?",
	})
	session := beads.Bead{
		ID:     "mc-session-1",
		Status: "open",
		Type:   "session",
		Metadata: map[string]string{
			"state":        "active",
			"session_name": "s-worker",
			"template":     "worker",
		},
	}

	input := buildAwakeInputFromReconciler(
		&config.City{Agents: []config.Agent{{Name: "worker"}}},
		[]beads.Bead{session},
		nil,
		nil,
		nil,
		nil,
		nil,
		[]wakeTarget{{session: &session, alive: true}},
		sp,
		now,
	)

	if !input.PendingSessions["s-worker"] {
		t.Fatalf("PendingSessions[s-worker] = false, want true")
	}
	decisions := ComputeAwakeSet(input)
	got := decisions["s-worker"]
	if !got.ShouldWake || got.Reason != "pending" {
		t.Fatalf("decision = %+v, want pending wake", got)
	}
}

func TestBuildAwakeInputFromReconcilerSkipsOnDemandNamedSessionsFromControllerDesired(t *testing.T) {
	now := time.Now().UTC()
	input := buildAwakeInputFromReconciler(
		&config.City{},
		nil,
		map[string]TemplateParams{
			"test-city--triage": {
				SessionName:         "test-city--triage",
				TemplateName:        "worker",
				ConfiguredNamedMode: "on_demand",
			},
			"worker-1": {
				SessionName:  "worker-1",
				TemplateName: "worker",
			},
		},
		nil,
		nil,
		nil,
		nil,
		nil,
		runtime.NewFake(),
		now,
	)

	if input.ControllerDesiredSessions["test-city--triage"] {
		t.Fatal("on-demand named session should not be treated as controller-desired wake")
	}
	if !input.ControllerDesiredSessions["worker-1"] {
		t.Fatal("ordinary desired session should be treated as controller-desired wake")
	}
}
