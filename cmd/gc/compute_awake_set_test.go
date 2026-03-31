package main

import (
	"testing"
	"time"
)

var now = time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC)

func assertAwake(t *testing.T, result map[string]AwakeDecision, sessionName string) {
	t.Helper()
	d, ok := result[sessionName]
	if !ok {
		t.Errorf("session %q not in result", sessionName)
		return
	}
	if !d.ShouldWake {
		t.Errorf("session %q should be awake but isn't (reason: %s)", sessionName, d.Reason)
	}
}

func assertAsleep(t *testing.T, result map[string]AwakeDecision, sessionName string) {
	t.Helper()
	d, ok := result[sessionName]
	if !ok {
		return // not in result = not awake = correct
	}
	if d.ShouldWake {
		t.Errorf("session %q should be asleep but is awake (reason: %s)", sessionName, d.Reason)
	}
}

// ---------------------------------------------------------------------------
// Named session (always)
// ---------------------------------------------------------------------------

func TestNamedAlways_AsleepWakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "asleep", NamedIdentity: "deacon"}},
		Now:           now,
	})
	assertAwake(t, result, "deacon")
}

func TestNamedAlways_ActiveStaysAwake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:          []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions:   []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads:    []AwakeSessionBead{{ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "active", NamedIdentity: "deacon"}},
		RunningSessions: map[string]bool{"deacon": true},
		Now:             now,
	})
	assertAwake(t, result, "deacon")
}

func TestNamedAlways_NoBead_CreatesAndWakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads:  []AwakeSessionBead{}, // no bead yet
		Now:           now,
	})
	// When there's no bead, the desired set uses the identity as a placeholder.
	// The result won't have a session entry (no bead = no decision),
	// but the desired set includes it. The caller (syncSessionBeads) handles creation.
	// This test validates the function doesn't crash with no beads.
	if len(result) != 0 {
		t.Errorf("expected empty result (no beads to decide on), got %d entries", len(result))
	}
}

func TestNamedAlways_Quarantined_DoesNotWake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "deacon"}},
		NamedSessions: []AwakeNamedSession{{Identity: "deacon", Template: "deacon", Mode: "always"}},
		SessionBeads: []AwakeSessionBead{{
			ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "asleep",
			NamedIdentity: "deacon", QuarantinedUntil: now.Add(5 * time.Minute),
		}},
		Now: now,
	})
	assertAsleep(t, result, "deacon")
}

func TestNamedAlways_TemplateRemoved_Orphan(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{}, // no agents — template removed
		NamedSessions: []AwakeNamedSession{},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "deacon", Template: "deacon", State: "asleep", NamedIdentity: "deacon"}},
		Now:           now,
	})
	assertAsleep(t, result, "deacon")
}

// ---------------------------------------------------------------------------
// Named session (on_demand)
// ---------------------------------------------------------------------------

func TestNamedOnDemand_NoWork_StaysAsleep(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions: []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "asleep", NamedIdentity: "hello-world/refinery"}},
		WorkBeads:     []AwakeWorkBead{}, // no work
		Now:           now,
	})
	assertAsleep(t, result, "hello-world--refinery")
}

func TestNamedOnDemand_AssigneeMatchesAlias_Wakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions: []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "asleep", NamedIdentity: "hello-world/refinery"}},
		WorkBeads:     []AwakeWorkBead{{ID: "hw-1", Assignee: "hello-world/refinery", Status: "open"}},
		Now:           now,
	})
	assertAwake(t, result, "hello-world--refinery")
}

func TestNamedOnDemand_WorkCompleted_Drains(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:          []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions:   []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:    []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "active", NamedIdentity: "hello-world/refinery"}},
		WorkBeads:       []AwakeWorkBead{}, // work done
		RunningSessions: map[string]bool{"hello-world--refinery": true},
		Now:             now,
	})
	assertAsleep(t, result, "hello-world--refinery")
}

func TestNamedOnDemand_NoDemand_ButAttached_StaysAwake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions:    []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:     []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "active", NamedIdentity: "hello-world/refinery"}},
		WorkBeads:        []AwakeWorkBead{},
		RunningSessions:  map[string]bool{"hello-world--refinery": true},
		AttachedSessions: map[string]bool{"hello-world--refinery": true},
		Now:              now,
	})
	assertAwake(t, result, "hello-world--refinery")
}

func TestNamedOnDemand_ScaleCheckIrrelevant(t *testing.T) {
	// scaleCheck > 0 for the template should NOT wake a named session.
	// Named sessions only wake via assignee match.
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions:    []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:     []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "asleep", NamedIdentity: "hello-world/refinery"}},
		ScaleCheckCounts: map[string]int{"hello-world/refinery": 1},
		Now:              now,
	})
	assertAsleep(t, result, "hello-world--refinery")
}

// ---------------------------------------------------------------------------
// Agent template (scaled)
// ---------------------------------------------------------------------------

func TestScaled_NoDemand_NoBeads(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 0},
		Now:              now,
	})
	if len(result) != 0 {
		t.Errorf("expected no decisions, got %d", len(result))
	}
}

func TestScaled_Demand1_NoBeads_NeedCreate(t *testing.T) {
	// No existing beads. scaleCheck = 1. The function can't create beads
	// (that's syncSessionBeads' job), but it should not crash.
	result := ComputeAwakeSet(AwakeInput{
		Agents:           []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	// No beads to decide on
	if len(result) != 0 {
		t.Errorf("expected no decisions (no beads), got %d", len(result))
	}
}

func TestScaled_Demand2_OneActive_WakeSecond(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active"},
			{ID: "mc-2", SessionName: "polecat-mc-2", Template: "hello-world/polecat", State: "asleep"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 2},
		RunningSessions:  map[string]bool{"polecat-mc-1": true},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-1")
	// mc-2 is asleep — should NOT be reused for new demand.
	// A new creating bead would be spawned by syncSessionBeads.
	assertAsleep(t, result, "polecat-mc-2")
}

func TestScaled_Demand1_TwoActive_DrainOne(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active"},
			{ID: "mc-2", SessionName: "polecat-mc-2", Template: "hello-world/polecat", State: "active"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		RunningSessions:  map[string]bool{"polecat-mc-1": true, "polecat-mc-2": true},
		Now:              now,
	})
	// One should wake, one should drain
	awakeCount := 0
	for _, d := range result {
		if d.ShouldWake {
			awakeCount++
		}
	}
	if awakeCount != 1 {
		t.Errorf("expected 1 awake session, got %d", awakeCount)
	}
}

func TestScaled_Demand0_OneActive_Drain(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 0},
		RunningSessions:  map[string]bool{"polecat-mc-1": true},
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-1")
}

func TestScaled_CreatingBead_Wakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "creating"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-1")
}

func TestScaled_AsleepEphemeral_NotReused(t *testing.T) {
	// scaleCheck=1, one asleep ephemeral exists.
	// Should NOT reuse the asleep one — it's a completed lifecycle.
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-old", SessionName: "polecat-mc-old", Template: "hello-world/polecat", State: "asleep"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-old")
}

// ---------------------------------------------------------------------------
// Manual session
// ---------------------------------------------------------------------------

func TestManual_ImplicitAgent_Wakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "creating", ManualSession: true},
		},
		Now: now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestManual_ExplicitAgent_Wakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "s-mc-1", Template: "hello-world/polecat", State: "creating", ManualSession: true},
		},
		Now: now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestManual_NoDemand_StaysAwake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active", ManualSession: true},
		},
		ScaleCheckCounts: map[string]int{"gascity/claude": 0},
		RunningSessions:  map[string]bool{"s-mc-1": true},
		Now:              now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestManual_BeadClosed_NotInResult(t *testing.T) {
	// Closed beads shouldn't be in SessionBeads input at all,
	// but if they are, they shouldn't wake.
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "closed", ManualSession: true},
		},
		Now: now,
	})
	assertAsleep(t, result, "s-mc-1")
}

func TestManual_PendingInteraction_StaysAwake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active", ManualSession: true},
		},
		RunningSessions: map[string]bool{"s-mc-1": true},
		PendingSessions: map[string]bool{"s-mc-1": true},
		Now:             now,
	})
	assertAwake(t, result, "s-mc-1")
}

// ---------------------------------------------------------------------------
// Cross-cutting: Dependencies
// ---------------------------------------------------------------------------

func TestDependency_DepRunning_Wakes(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{
			{QualifiedName: "hello-world/witness"},
			{QualifiedName: "hello-world/polecat", DependsOn: []string{"hello-world/witness"}},
		},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-w", SessionName: "hello-world--witness", Template: "hello-world/witness", State: "active"},
			{ID: "mc-p", SessionName: "polecat-mc-p", Template: "hello-world/polecat", State: "creating"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		RunningSessions:  map[string]bool{"hello-world--witness": true},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-p")
}

func TestDependency_DepNotRunning_DoesNotWake(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{
			{QualifiedName: "hello-world/witness"},
			{QualifiedName: "hello-world/polecat", DependsOn: []string{"hello-world/witness"}},
		},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-w", SessionName: "hello-world--witness", Template: "hello-world/witness", State: "asleep"},
			{ID: "mc-p", SessionName: "polecat-mc-p", Template: "hello-world/polecat", State: "creating"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		RunningSessions:  map[string]bool{}, // witness not running
		Now:              now,
	})
	assertAsleep(t, result, "polecat-mc-p")
}

func TestScaled_MultipleSessions_CappedByScaleCheck(t *testing.T) {
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "active"},
			{ID: "mc-2", SessionName: "polecat-mc-2", Template: "hello-world/polecat", State: "active"},
			{ID: "mc-3", SessionName: "polecat-mc-3", Template: "hello-world/polecat", State: "active"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 2},
		RunningSessions:  map[string]bool{"polecat-mc-1": true, "polecat-mc-2": true, "polecat-mc-3": true},
		Now:              now,
	})
	awakeCount := 0
	for _, d := range result {
		if d.ShouldWake {
			awakeCount++
		}
	}
	if awakeCount != 2 {
		t.Errorf("expected 2 awake (capped by scaleCheck), got %d", awakeCount)
	}
}

// ---------------------------------------------------------------------------
// Bug regression tests
// ---------------------------------------------------------------------------

func TestRegression_PoolManagedCreatingBeadEntersDesired(t *testing.T) {
	// Bug: pool-managed creating beads were skipped in discoverSessionBeadsWithRoots
	// and never entered desiredState. The reconciler orphaned them.
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "hello-world/polecat"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "polecat-mc-1", Template: "hello-world/polecat", State: "creating"},
		},
		ScaleCheckCounts: map[string]int{"hello-world/polecat": 1},
		Now:              now,
	})
	assertAwake(t, result, "polecat-mc-1")
}

func TestRegression_ManualSessionNotDrainedAsNoWakeReason(t *testing.T) {
	// Bug: manual sessions on implicit agents got configEligible=false
	// and were drained with "no-wake-reason" on the first tick.
	result := ComputeAwakeSet(AwakeInput{
		Agents: []AwakeAgent{{QualifiedName: "gascity/claude"}},
		SessionBeads: []AwakeSessionBead{
			{ID: "mc-1", SessionName: "s-mc-1", Template: "gascity/claude", State: "active", ManualSession: true},
		},
		ScaleCheckCounts: map[string]int{"gascity/claude": 0}, // no pool demand
		RunningSessions:  map[string]bool{"s-mc-1": true},
		Now:              now,
	})
	assertAwake(t, result, "s-mc-1")
}

func TestRegression_OnDemandRefineryWakesWhenAssigneeMatchesAlias(t *testing.T) {
	// Bug: on_demand refinery didn't wake because work_query checked
	// gc.routed_to (polecat) not Assignee (refinery). namedWorkReady
	// was never set.
	result := ComputeAwakeSet(AwakeInput{
		Agents:        []AwakeAgent{{QualifiedName: "hello-world/refinery"}},
		NamedSessions: []AwakeNamedSession{{Identity: "hello-world/refinery", Template: "hello-world/refinery", Mode: "on_demand"}},
		SessionBeads:  []AwakeSessionBead{{ID: "mc-1", SessionName: "hello-world--refinery", Template: "hello-world/refinery", State: "asleep", NamedIdentity: "hello-world/refinery"}},
		WorkBeads:     []AwakeWorkBead{{ID: "hw-1", Assignee: "hello-world/refinery", Status: "open"}},
		Now:           now,
	})
	assertAwake(t, result, "hello-world--refinery")
}
