package main

import "testing"

// TestShouldWrap pins the containment decision matrix — the analogue of
// the 8-scenario bash self-test for gc_test_slice_should_wrap.
func TestShouldWrap(t *testing.T) {
	cases := []struct {
		name         string
		goos         string
		optedOut     bool
		alreadyInJob bool
		want         bool
	}{
		{name: "windows wraps", goos: "windows", want: true},
		{name: "opt-out", goos: "windows", optedOut: true, want: false},
		{name: "nested runner guard", goos: "windows", alreadyInJob: true, want: false},
		{name: "opt-out beats nested state", goos: "windows", optedOut: true, alreadyInJob: true, want: false},
		{name: "linux is slice territory", goos: "linux", want: false},
		{name: "darwin unwrapped", goos: "darwin", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldWrap(tc.goos, tc.optedOut, tc.alreadyInJob); got != tc.want {
				t.Fatalf("shouldWrap(%q, optedOut=%v, inJob=%v) = %v, want %v", tc.goos, tc.optedOut, tc.alreadyInJob, got, tc.want)
			}
		})
	}
}

// TestRecommendedJobs pins the shard-parallelism math, mirroring
// scripts/test-local-job-count: min(cpus, mem/4GiB, 16), at least 1.
func TestRecommendedJobs(t *testing.T) {
	const gib = 1 << 30
	cases := []struct {
		name string
		cpus int
		mem  uint64
		want int
	}{
		{name: "memory constrains", cpus: 32, mem: 24 * gib, want: 6},
		{name: "cpus constrain", cpus: 4, mem: 64 * gib, want: 4},
		{name: "cap at 16", cpus: 64, mem: 256 * gib, want: 16},
		{name: "unknown memory constrains nothing", cpus: 8, mem: 0, want: 8},
		{name: "tiny memory still one job", cpus: 8, mem: 1 * gib, want: 1},
		{name: "zero cpus still one job", cpus: 0, mem: 0, want: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := recommendedJobs(tc.cpus, tc.mem); got != tc.want {
				t.Fatalf("recommendedJobs(%d, %d) = %d, want %d", tc.cpus, tc.mem, got, tc.want)
			}
		})
	}
}

// TestDefaultJobMemory pins the 75%-of-available derivation.
func TestDefaultJobMemory(t *testing.T) {
	if got := defaultJobMemory(64 << 30); got != 48<<30 {
		t.Fatalf("defaultJobMemory(64GiB) = %d, want 48GiB", got)
	}
	if got := defaultJobMemory(0); got != 0 {
		t.Fatalf("defaultJobMemory(0) = %d, want 0", got)
	}
}
