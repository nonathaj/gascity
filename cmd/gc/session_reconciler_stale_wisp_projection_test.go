package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
)

// Focused proof command:
//
//	go test ./cmd/gc/ -run TestSessionHasOpenAssignedWispWork -v
//
// These tests cover the fe-bjz claim loop (REQ-002/AC-1, plan work item W3B).
// The controller's in-process CachingStore kept serving a wisp row as claimable
// after the store had closed it, because the wisp probe trusted a positive cache
// answer and never consulted the backing store. W3A attributed the loop to that
// positive-only short-circuit; these tests pin the store-truth contract in its
// place.

// stalePrimedWispCache builds a CachingStore whose in-memory projection still
// holds a wisp bead as open-and-assigned after the backing store has closed it.
// Closing through the backing only — never through the cache — reproduces the
// incident shape, where the row read CLOSED for 13 days while the projection
// went on offering it.
func stalePrimedWispCache(t *testing.T, assignee string) (*beads.CachingStore, beads.Bead) {
	t.Helper()

	backing := beads.NewMemStore()
	bead, err := backing.Create(beads.Bead{
		Title:     "mol-dog-stale-db",
		Type:      "task",
		Status:    "open",
		Assignee:  assignee,
		Ephemeral: true, // wisp tier: the probe under test reads TierWisps
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cache := beads.NewCachingStoreForTest(backing, nil)
	if err := cache.PrimeActive(); err != nil {
		t.Fatalf("PrimeActive: %v", err)
	}
	if err := backing.Close(bead.ID); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return cache, bead
}

// staleWispQuery is the claimable-set query the probe issues, reused by the
// tests to inspect what the projection would still serve.
func staleWispQuery(assignee string) beads.ListQuery {
	return beads.ListQuery{Assignee: assignee, Status: "open", TierMode: beads.TierWisps}
}

// TestSessionHasOpenAssignedWispWorkStoreTruthWins asserts that a closed row is
// not reported as open assigned wisp work, even while the projection still
// carries it. This is the serve half of AC-1.
func TestSessionHasOpenAssignedWispWorkStoreTruthWins(t *testing.T) {
	const assignee = "fe-eov5gu"
	cache, _ := stalePrimedWispCache(t, assignee)

	// Precondition: without a genuinely stale projection the test proves nothing.
	items, ok := cache.CachedList(staleWispQuery(assignee))
	if !ok || len(items) != 1 {
		t.Fatalf("precondition: projection should still serve the closed row; ok=%v items=%d", ok, len(items))
	}

	has, err := sessionHasOpenAssignedWispWork(cache, assignee, "open")
	if err != nil {
		t.Fatalf("sessionHasOpenAssignedWispWork: %v", err)
	}
	if has {
		t.Fatal("closed row served as open assigned wisp work: a positive cache answer was trusted over store truth")
	}
}

// TestSessionHasOpenAssignedWispWorkReArms drives two consecutive ticks and
// asserts the bead is absent from the claimable set on tick n+1 — the re-arm
// half of AC-1. Without invalidation the next tick re-serves the identical
// entry and the loop continues.
func TestSessionHasOpenAssignedWispWorkReArms(t *testing.T) {
	const assignee = "fe-eov5gu"
	cache, bead := stalePrimedWispCache(t, assignee)

	for tick, label := range []string{"tick n", "tick n+1"} {
		has, err := sessionHasOpenAssignedWispWork(cache, assignee, "open")
		if err != nil {
			t.Fatalf("%s (%d): sessionHasOpenAssignedWispWork: %v", label, tick, err)
		}
		if has {
			t.Fatalf("%s: closed row still reported as claimable work", label)
		}
	}

	// The projection must no longer offer the row it lost on: corrected to the
	// closed truth, or dropped. Either way it leaves the claimable set.
	items, _ := cache.CachedList(staleWispQuery(assignee))
	for _, item := range items {
		if item.ID == bead.ID {
			t.Fatalf("stale projection entry survived the store-truth read; the next tick would re-serve %s", bead.ID)
		}
	}
}
