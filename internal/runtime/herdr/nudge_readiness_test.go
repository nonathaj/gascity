package herdr

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
)

// TestNudgeReadinessFromStatus pins the whole mapping in one place: herdr's
// five statuses, and the rule that anything else holds as unknown.
func TestNudgeReadinessFromStatus(t *testing.T) {
	cases := []struct {
		status string
		want   runtime.NudgeReadiness
	}{
		{"idle", runtime.NudgeReady},
		{"done", runtime.NudgeReady},
		{"working", runtime.NudgeBusy},
		{"blocked", runtime.NudgeBlocked},
		{"unknown", runtime.NudgeUnclassified},
		// Not herdr statuses today. A status herdr adds later must hold.
		{"compacting", runtime.NudgeUnclassified},
		{"", runtime.NudgeUnclassified},
	}
	for _, tc := range cases {
		if got := nudgeReadinessFromStatus(tc.status); got != tc.want {
			t.Errorf("nudgeReadinessFromStatus(%q) = %q, want %q", tc.status, got, tc.want)
		}
	}
}

// TestNudgeReadinessAbsentAgentIsNoAgent: a name herdr does not know is
// "nothing to ask about", not an error and not a state.
func TestNudgeReadinessAbsentAgentIsNoAgent(t *testing.T) {
	f := newFakeHerdr(t, "") // its `agent get` reports not_found and `agent list` is empty
	p := newFakeStartProvider(t, f)
	readiness, status, err := p.NudgeReadiness(context.Background(), "gastown__worker")
	if err != nil || readiness != runtime.NudgeNoAgent || status != "" {
		t.Fatalf("NudgeReadiness = %q, %q, %v; want no_agent, \"\", nil", readiness, status, err)
	}
}

// TestNudgeReadinessTransportFailureIsAnError: when herdr cannot be reached
// there is no verdict. In particular the answer is not "ready".
func TestNudgeReadinessTransportFailureIsAnError(t *testing.T) {
	p := New("teststart", t.TempDir(), "/city/root", 0, 0)
	p.c.bin = "/nonexistent/herdr-binary-for-test"
	readiness, _, err := p.NudgeReadiness(context.Background(), "gastown__worker")
	if err == nil {
		t.Fatalf("NudgeReadiness = %q with no error, want an error when herdr cannot be run", readiness)
	}
	if readiness == runtime.NudgeReady {
		t.Fatal("an unreachable herdr read as ready")
	}
}
