package main

import (
	"bytes"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// The gem-federation incident (gcty-xvla) through the real desired-state
// build: default demand probes over a real store, pool sessions holding
// in-progress work, and a workspace cap with one slot left. The template listed
// first keeps a backlog; the template listed second has one ready bead and no
// sessions. The free slot must start the second pool, not grow the first.
func TestBuildDesiredState_WorkspaceCapStartsColdPoolBeforeGrowingBusyPool(t *testing.T) {
	const busy, cold = "busy-rig/worker", "cold-rig/worker"
	cityPath := t.TempDir()
	store := beads.NewMemStore()
	create := func(b beads.Bead) {
		t.Helper()
		if _, err := store.Create(b); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 2; i++ {
		alias := fmt.Sprintf("%s-%d", busy, i)
		create(beads.Bead{
			Title:  alias,
			Type:   sessionBeadType,
			Labels: []string{sessionBeadLabel, "agent:" + alias, "template:" + busy},
			Metadata: map[string]string{
				"template":             busy,
				"agent_name":           alias,
				"alias":                alias,
				"session_name":         fmt.Sprintf("s-busy-%d", i),
				"state":                "awake",
				poolManagedMetadataKey: boolMetadata(true),
				"pool_slot":            strconv.Itoa(i),
			},
		})
		create(beads.Bead{
			Title:    fmt.Sprintf("busy in-progress %d", i),
			Type:     "task",
			Status:   "in_progress",
			Assignee: alias,
			Metadata: map[string]string{"gc.routed_to": busy},
		})
	}
	for i := 1; i <= 5; i++ {
		create(beads.Bead{
			Title:    fmt.Sprintf("busy backlog %d", i),
			Type:     "task",
			Status:   "open",
			Metadata: map[string]string{"gc.routed_to": busy},
		})
	}
	create(beads.Bead{
		Title:    "cold ready",
		Type:     "task",
		Status:   "open",
		Metadata: map[string]string{"gc.routed_to": cold},
	})
	cfg := &config.City{
		Workspace: config.Workspace{Name: "test-city", MaxActiveSessions: intPtr(3)},
		Agents: []config.Agent{
			{Name: "worker", Dir: "busy-rig", StartCommand: "true"},
			{Name: "worker", Dir: "cold-rig", StartCommand: "true"},
		},
	}

	var stderr bytes.Buffer
	result := buildDesiredState("test-city", cityPath, time.Now().UTC(), cfg, runtime.NewFake(), store, &stderr)

	if got := result.ScaleCheckCounts; got[busy] != 5 || got[cold] != 1 {
		t.Fatalf("scale_check = %v, want %s=5 %s=1 (fixture must present both demands)", got, busy, cold)
	}
	perTemplate := make(map[string]int)
	for _, tp := range result.State {
		perTemplate[tp.TemplateName]++
	}
	if perTemplate[cold] != 1 || perTemplate[busy] != 2 {
		t.Fatalf("desired sessions per template = %v, want %s=2 (its in-progress work) and %s=1 (the free slot); keys=%v stderr=%q",
			perTemplate, busy, cold, mapKeys(result.State), stderr.String())
	}
}
