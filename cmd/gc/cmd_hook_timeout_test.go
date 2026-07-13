package main

import (
	"testing"
	"time"
)

// TestWorkQueryTimeoutsAccommodateMultiRoundTripProbe guards the work-query
// timeout budget. The default work-probe (config.Agent.EffectiveWorkQuery)
// issues ~6 sequential bd/store round-trips — three session identifiers across
// the in-progress and ready assigned tiers — before the pool-demand tier that
// finds routed work. On a multi-rig dolt city under concurrent load each
// round-trip costs several seconds, so at the prior 30s work-query subprocess
// budget the probe was killed before reaching pool-demand and pool operators
// (gc.run-operator) were starved of work they had already been routed. Keep the
// subprocess budget generous enough to clear the realistic loaded cost.
//
// This guards hookWorkQueryTimeout, the cap that actually bounds the work query
// (shellWorkQueryWithEnv in `gc hook` and the workflow serve loop). It does not
// constrain defaultHookRunTimeout: that budget bounds the separate `gc hook run`
// managed-hook wrapper (nudge drain / mail check) and does not enclose the work
// query, so the two are intentionally independent and not asserted against each
// other here.
func TestWorkQueryTimeoutsAccommodateMultiRoundTripProbe(t *testing.T) {
	// minProbeBudget is the remediation target, not merely the old cap: keeping it
	// at 60s means a regression of hookWorkQueryTimeout back to the known-bad 30s
	// budget (which starved pool operators) fails this guard rather than passing it.
	const minProbeBudget = 60 * time.Second

	if hookWorkQueryTimeout < minProbeBudget {
		t.Errorf("hookWorkQueryTimeout = %s, want >= %s (multi-round-trip probe budget)", hookWorkQueryTimeout, minProbeBudget)
	}
}

// TestResolveHookWorkQueryTimeoutEnvOverride covers the
// GC_HOOK_WORK_QUERY_TIMEOUT override for hosts where each bd round-trip is
// expensive (per-call doltlite fallback on Windows costs ~4s, so the composite
// probe needs more than the 60s default). Invalid, non-positive, or absent
// values must fall back to the default rather than disable or zero the cap.
func TestResolveHookWorkQueryTimeoutEnvOverride(t *testing.T) {
	const fallback = 60 * time.Second
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset", env: "", want: fallback},
		{name: "valid seconds", env: "180s", want: 180 * time.Second},
		{name: "valid minutes", env: "3m", want: 3 * time.Minute},
		{name: "padded", env: "  90s  ", want: 90 * time.Second},
		{name: "not a duration", env: "banana", want: fallback},
		{name: "bare number", env: "180", want: fallback},
		{name: "zero", env: "0s", want: fallback},
		{name: "negative", env: "-30s", want: fallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GC_HOOK_WORK_QUERY_TIMEOUT", tc.env)
			if got := resolveHookWorkQueryTimeout(); got != tc.want {
				t.Errorf("resolveHookWorkQueryTimeout() with %q = %s, want %s", tc.env, got, tc.want)
			}
		})
	}
}
