package herdr

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
)

// TestLiveNudgePollerDonePane reads NudgeReadiness — what the nudge poller's
// delivery gate asks — for a real pane on a real herdr. Opt-in live tier: see
// requireLiveHerdr. It starts and tears down its own isolated herdr session.
//
// What it can and cannot show, measured on herdr 0.9.0:
//
//   - `pane report-agent` accepts idle, working, blocked and unknown. It
//     REFUSES done: herdr gives that status only to an agent it detected
//     itself, after a turn it watched finish. So a test cannot put a pane in
//     the state gf-pdx7a8 is about. The done case is pinned by the stubbed
//     poller test in cmd/gc (TestNudgePoller_HerdrDonePaneQueuedNudgeIsDelivered),
//     whose replies are the 0.9.0 shape read from a live city, and by the live
//     proof on a parked session after the rollout.
//   - What this test does pin is that a REAL herdr's replies are read
//     correctly for every state that can be forced: idle is ready; working,
//     blocked and unknown are not, each under its own status word; and the
//     reading follows the pane back to idle. If a later herdr accepts a
//     reported done, it must read ready.
func TestLiveNudgePollerDonePane(t *testing.T) {
	requireLiveHerdr(t)

	const session = "gctest-nudge-done-live"
	const name = "nudge-done"
	p := New(session, t.TempDir(), t.TempDir(), 0, 0)
	_ = p.c.stopServer() // clear any leftover server from a crashed prior run
	t.Cleanup(func() { _ = p.TeardownServer() })
	if err := p.ConfigureServer(); err != nil {
		t.Fatalf("ConfigureServer: %v", err)
	}
	ctx := context.Background()
	// A pane running cat: no agent integration of its own.
	if err := p.Start(ctx, name, liveActivityCfg(t)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop(name) })
	paneID := firstPaneID(t, session)

	for _, tc := range []struct {
		state string
		want  runtime.NudgeReadiness
	}{
		{"idle", runtime.NudgeReady},
		{"working", runtime.NudgeBusy},
		{"blocked", runtime.NudgeBlocked},
		{"unknown", runtime.NudgeUnclassified},
		{"idle", runtime.NudgeReady}, // and back: the reading is not sticky
	} {
		if err := reportAgentState(session, paneID, name, tc.state); err != nil {
			t.Fatal(err)
		}
		readiness, status, err := p.NudgeReadiness(ctx, name)
		t.Logf("live herdr: reported %s → NudgeReadiness %q, status %q, err %v", tc.state, readiness, status, err)
		if err != nil || readiness != tc.want || status != tc.state {
			t.Fatalf("NudgeReadiness for a real pane reported %s = %q, %q, %v; want %q, %q, nil", tc.state, readiness, status, err, tc.want, tc.state)
		}
	}

	if err := reportAgentState(session, paneID, name, "done"); err != nil {
		t.Logf("live herdr refuses a reported done, as on 0.9.0: %v", err)
		return
	}
	if readiness, status, err := p.NudgeReadiness(ctx, name); err != nil || readiness != runtime.NudgeReady || status != "done" {
		t.Fatalf("this herdr accepts a reported done, and NudgeReadiness says %q, %q, %v; want ready, done, nil", readiness, status, err)
	}
}
