package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

// Regression (gcty-xvla): when shared capacity is scarce, the slots left after
// the resume tier were handed out greedily in config order. A template listed
// early with a standing backlog took every slot the resume tier freed, and a
// template listed later sat at zero sessions for as long as that backlog
// lasted. That is the shape that kept gem-federation (second-to-last in the
// federation's city.toml) dark while earlier rigs held the city's sessions.
func TestComputePoolDesiredStates_ScarceCapacityFillsFewestSessionsFirst(t *testing.T) {
	cases := []struct {
		name       string
		workspace  *int
		rigs       []config.Rig
		agents     []config.Agent
		work       []beads.Bead
		sessions   []beads.Bead
		scaleCheck map[string]int
		want       map[string]int
	}{
		{
			name:      "freed workspace slot goes to the template at zero sessions",
			workspace: intPtr(3),
			agents: []config.Agent{
				poolAgent("worker", "busy-rig", nil, 0),
				poolAgent("worker", "cold-rig", nil, 0),
			},
			work: []beads.Bead{
				workBead("w1", "busy-rig/worker", "sess-1", "in_progress", 2),
				workBead("w2", "busy-rig/worker", "sess-2", "in_progress", 2),
			},
			sessions:   []beads.Bead{sessionBead("sess-1", "open"), sessionBead("sess-2", "open")},
			scaleCheck: map[string]int{"busy-rig/worker": 5, "cold-rig/worker": 1},
			want:       map[string]int{"busy-rig/worker": 2, "cold-rig/worker": 1},
		},
		{
			name: "freed rig slot goes to the template at zero sessions",
			rigs: []config.Rig{{Name: "rig", Path: "/tmp/rig", MaxActiveSessions: intPtr(2)}},
			agents: []config.Agent{
				poolAgent("builder", "rig", nil, 0),
				poolAgent("reviewer", "rig", nil, 0),
			},
			work:       []beads.Bead{workBead("w1", "rig/builder", "sess-1", "in_progress", 2)},
			sessions:   []beads.Bead{sessionBead("sess-1", "open")},
			scaleCheck: map[string]int{"rig/builder": 4, "rig/reviewer": 1},
			want:       map[string]int{"rig/builder": 1, "rig/reviewer": 1},
		},
		{
			name:      "cold templates are raised level by level, config order breaking ties",
			workspace: intPtr(4),
			agents: []config.Agent{
				poolAgent("a", "rig", nil, 0),
				poolAgent("b", "rig", nil, 0),
				poolAgent("c", "rig", nil, 0),
			},
			scaleCheck: map[string]int{"rig/a": 5, "rig/b": 5, "rig/c": 1},
			want:       map[string]int{"rig/a": 2, "rig/b": 1, "rig/c": 1},
		},
		{
			name:      "colder templates catch up before a warm template grows",
			workspace: intPtr(5),
			agents: []config.Agent{
				poolAgent("a", "rig", nil, 0),
				poolAgent("b", "rig", nil, 0),
				poolAgent("c", "rig", nil, 0),
			},
			work: []beads.Bead{
				workBead("w1", "rig/a", "sess-1", "in_progress", 2),
				workBead("w2", "rig/a", "sess-2", "in_progress", 2),
			},
			sessions:   []beads.Bead{sessionBead("sess-1", "open"), sessionBead("sess-2", "open")},
			scaleCheck: map[string]int{"rig/a": 5, "rig/b": 5, "rig/c": 5},
			want:       map[string]int{"rig/a": 2, "rig/b": 2, "rig/c": 1},
		},
		{
			name:      "a template's own agent cap hands its share to the others",
			workspace: intPtr(4),
			agents: []config.Agent{
				poolAgent("a", "rig", intPtr(1), 0),
				poolAgent("b", "rig", nil, 0),
			},
			scaleCheck: map[string]int{"rig/a": 5, "rig/b": 5},
			want:       map[string]int{"rig/a": 1, "rig/b": 3},
		},
		{
			name:      "uncontended capacity serves every template's full demand",
			workspace: intPtr(20),
			agents: []config.Agent{
				poolAgent("a", "rig", nil, 0),
				poolAgent("b", "rig", intPtr(2), 0),
				poolAgent("c", "rig", nil, 0),
			},
			work:       []beads.Bead{workBead("w1", "rig/a", "sess-1", "in_progress", 2)},
			sessions:   []beads.Bead{sessionBead("sess-1", "open")},
			scaleCheck: map[string]int{"rig/a": 6, "rig/b": 5, "rig/c": 3},
			want:       map[string]int{"rig/a": 7, "rig/b": 2, "rig/c": 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.City{
				Workspace: config.Workspace{MaxActiveSessions: tc.workspace},
				Rigs:      tc.rigs,
				Agents:    tc.agents,
			}
			templates := make([]string, 0, len(cfg.Agents))
			for i := range cfg.Agents {
				templates = append(templates, cfg.Agents[i].QualifiedName())
			}
			trace := newPoolDesiredStateTestTrace(templates...)

			counts := PoolDesiredCounts(computePoolDesiredStates(cfg, tc.work, sessionInfosFromBeads(tc.sessions), tc.scaleCheck, nil, trace))

			for template, want := range tc.want {
				if got := counts[template]; got != want {
					t.Errorf("desired[%s] = %d, want %d (counts=%v)", template, got, want, counts)
				}
			}
			capRejections := trace.decisionCounts[string(TraceSitePoolAgentCap)] +
				trace.decisionCounts[string(TraceSitePoolRigCap)] +
				trace.decisionCounts[string(TraceSitePoolWorkspaceCap)]
			if capRejections != 0 {
				t.Errorf("cap rejections = %d, want 0: fair shares must fit the caps before requests are materialized", capRejections)
			}
		})
	}
}

// Sessions that are already starting (or freshly started and not yet claimed)
// are spent capacity. Fair sharing distributes fresh creates only; it must not
// drop an in-flight session to fund another template's first create, or every
// scarce tick would churn a session that is already coming up.
func TestComputePoolDesiredStates_FairShareKeepsInFlightSessions(t *testing.T) {
	workspaceMax := 2
	cfg := &config.City{
		Workspace: config.Workspace{MaxActiveSessions: &workspaceMax},
		Agents: []config.Agent{
			poolAgent("warm", "", nil, 0),
			poolAgent("cold", "", nil, 0),
		},
	}
	sessions := []beads.Bead{
		pendingPoolSessionBeadForTemplate("pending-1", "warm"),
		pendingPoolSessionBeadForTemplate("pending-2", "warm"),
	}
	scaleCheck := map[string]int{"warm": 2, "cold": 1}

	result := ComputePoolDesiredStates(cfg, nil, sessionInfosFromBeads(sessions), scaleCheck)

	byTemplate := make(map[string][]SessionRequest, len(result))
	for _, state := range result {
		byTemplate[state.Template] = state.Requests
	}
	warm := byTemplate["warm"]
	if len(warm) != 2 || warm[0].SessionBeadID != "pending-1" || warm[1].SessionBeadID != "pending-2" {
		t.Fatalf("warm requests = %+v, want both in-flight sessions kept", warm)
	}
	if cold := byTemplate["cold"]; len(cold) != 0 {
		t.Fatalf("cold requests = %+v, want none while in-flight sessions hold the capacity", cold)
	}
}

// The new-demand cap trace must still name the capacity a template yielded to,
// even when the blocking claims belong to templates listed after it.
func TestComputePoolDesiredStates_FairShareTraceNamesYieldedCapacity(t *testing.T) {
	workspaceMax := 3
	cfg := &config.City{
		Workspace: config.Workspace{MaxActiveSessions: &workspaceMax},
		Agents: []config.Agent{
			poolAgent("worker", "busy-rig", nil, 0),
			poolAgent("worker", "cold-rig", nil, 0),
		},
	}
	work := []beads.Bead{
		workBead("w1", "busy-rig/worker", "sess-1", "in_progress", 2),
		workBead("w2", "busy-rig/worker", "sess-2", "in_progress", 2),
	}
	sessions := []beads.Bead{sessionBead("sess-1", "open"), sessionBead("sess-2", "open")}
	trace := newPoolDesiredStateTestTrace("busy-rig/worker", "cold-rig/worker")

	computePoolDesiredStates(cfg, work, sessionInfosFromBeads(sessions), map[string]int{"busy-rig/worker": 5, "cold-rig/worker": 1}, nil, trace)

	if got := trace.decisionCounts[string(TraceSitePoolNewDemandCap)]; got != 1 {
		t.Fatalf("new-demand cap decisions = %d, want 1 (only the yielding template); records=%#v", got, trace.records)
	}
	rec := poolTraceDecision(t, trace, TraceSitePoolNewDemandCap)
	if rec.Template != "busy-rig/worker" {
		t.Fatalf("new-demand cap template = %q, want busy-rig/worker", rec.Template)
	}
	if got := rec.Fields["active_capacity_kind"]; got != string(TraceReasonWorkspaceCap) {
		t.Fatalf("active_capacity_kind = %#v, want %q", got, TraceReasonWorkspaceCap)
	}
	for key, want := range map[string]int{
		"scale_check":  5,
		"accepted_new": 0,
		"blocked_new":  5,
		"current":      3,
		"max":          3,
	} {
		if got := poolTraceFieldInt(t, rec.Fields, key); got != want {
			t.Fatalf("%s = %d, want %d", key, got, want)
		}
	}
	if got := poolTraceFieldStrings(t, rec.Fields, "blocking_work_beads"); len(got) != 2 || got[0] != "w1" || got[1] != "w2" {
		t.Fatalf("blocking_work_beads = %#v, want [w1 w2]", got)
	}
}

func pendingPoolSessionBeadForTemplate(id, template string) beads.Bead {
	session := pendingPoolSessionBead(id)
	session.Labels = []string{sessionBeadLabel, "template:" + template}
	session.Metadata["template"] = template
	session.Metadata["session_name"] = PoolSessionName(template, id)
	return session
}
