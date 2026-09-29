// Fork-local pins: an unresolved nudge target never dead-letters a queued
// nudge. Offered upstream.

package main

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/config"
)

// TestQueuedNudgeClaimableForTarget_UnresolvedTargetClaimsOnlyUnfencedItems
// pins that an unresolved target (no session identity, no continuation epoch)
// claims only unfenced items and never consults the liveness census.
// Everything it claims passes the split's fence check, so it can never
// dead-letter a queued nudge.
func TestQueuedNudgeClaimableForTarget_UnresolvedTargetClaimsOnlyUnfencedItems(t *testing.T) {
	unresolved := nudgeTarget{agent: config.Agent{Name: "worker"}}
	census := func() map[string]struct{} {
		t.Errorf("liveness census consulted for an unresolved target")
		return map[string]struct{}{"gc-1": {}}
	}
	tests := []struct {
		name string
		item queuedNudge
		want bool
	}{
		{name: "unfenced", item: queuedNudge{Agent: "worker"}, want: true},
		{name: "epoch-only", item: queuedNudge{Agent: "worker", ContinuationEpoch: "1"}, want: false},
		{name: "session-only", item: queuedNudge{Agent: "worker", SessionID: "gc-1"}, want: false},
		{name: "session-and-epoch", item: queuedNudge{Agent: "worker", SessionID: "gc-1", ContinuationEpoch: "1"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := queuedNudgeClaimableForTarget(unresolved, tt.item, census)
			if got != tt.want {
				t.Fatalf("claimable = %v, want %v", got, tt.want)
			}
			if !got {
				return
			}
			if _, rejected := splitQueuedNudgesForTarget(unresolved, []queuedNudge{tt.item}); len(rejected) != 0 {
				t.Fatalf("claimed item rejected by split: %#v", rejected)
			}
		})
	}
}

// TestClaimDueQueuedNudgesForTarget_UnresolvedTargetLeavesFencedItemsPending
// pins the same invariant through the real flock'd state.json claim path: an
// unresolved target claims only the unfenced item, and every fenced item stays
// pending instead of being dead-lettered.
func TestClaimDueQueuedNudgesForTarget_UnresolvedTargetLeavesFencedItemsPending(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	dir := t.TempDir()
	now := time.Now().Add(-time.Minute)
	for _, it := range []struct {
		id   string
		opts queuedNudgeOptions
	}{
		{"n-unfenced", queuedNudgeOptions{}},
		{"n-epoch-only", queuedNudgeOptions{ContinuationEpoch: "1"}},
		{"n-session-only", queuedNudgeOptions{SessionID: "gc-1"}},
		{"n-both", queuedNudgeOptions{SessionID: "gc-1", ContinuationEpoch: "1"}},
	} {
		it.opts.ID = it.id
		if err := enqueueQueuedNudge(dir, newQueuedNudgeWithOptions("worker", it.id, "session", now, it.opts)); err != nil {
			t.Fatalf("enqueue %s: %v", it.id, err)
		}
	}
	prev := liveNudgeFenceSessionIDs
	t.Cleanup(func() { liveNudgeFenceSessionIDs = prev })
	liveNudgeFenceSessionIDs = func(string) map[string]struct{} {
		t.Errorf("liveness census consulted for an unresolved target")
		return nil
	}
	claimed, err := claimDueQueuedNudgesForTarget(dir, nudgeTarget{agent: config.Agent{Name: "worker"}}, time.Now())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if ids := queuedNudgeIDs(claimed); len(ids) != 1 || ids[0] != "n-unfenced" {
		t.Fatalf("claimed = %v, want [n-unfenced]", ids)
	}
	pending, _, dead, err := listQueuedNudges(dir, "worker", time.Now())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 3 || len(dead) != 0 {
		t.Fatalf("pending=%d dead=%d, want 3/0 (fenced items stay pending, nothing dead-lettered)", len(pending), len(dead))
	}
}

// TestClaimThenSplit_DeadLetterRequiresEpochOnlyTargetAndItem pins that, across
// every (target, item) fence shape, exactly one pair is claimed and then
// rejected by the split (dead-lettered): an epoch-only target
// (sessionID == "", continuationEpoch != "") claiming an epoch-only item whose
// epoch differs. It fails if claim-then-split gains another dead-letter pair or
// loses this one.
func TestClaimThenSplit_DeadLetterRequiresEpochOnlyTargetAndItem(t *testing.T) {
	census := func() map[string]struct{} { return map[string]struct{}{"gc-1": {}} }
	var got []string
	for _, tsid := range []string{"", "gc-1"} {
		for _, tep := range []string{"", "2"} {
			tgt := nudgeTarget{agent: config.Agent{Name: "worker"}, sessionID: tsid, continuationEpoch: tep}
			for _, isid := range []string{"", "gc-1", "gc-0"} {
				for _, iep := range []string{"", "1", "2"} {
					item := queuedNudge{ID: "n", Agent: "worker", SessionID: isid, ContinuationEpoch: iep}
					if !queuedNudgeClaimableForTarget(tgt, item, census) {
						continue
					}
					if _, rejected := splitQueuedNudgesForTarget(tgt, []queuedNudge{item}); len(rejected) > 0 {
						got = append(got, fmt.Sprintf("target(sid=%q,ep=%q) item(sid=%q,ep=%q)", tsid, tep, isid, iep))
					}
				}
			}
		}
	}
	sort.Strings(got)
	want := []string{`target(sid="",ep="2") item(sid="",ep="1")`}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("dead-letter pairs =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
