package main

import "testing"

func TestDoltServerEnv_AppendsDefaultWhenMissing(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HOME=/home/test"}
	out := doltServerEnv(parent)

	want := "DOLT_GC_SCHEDULER=NONE"
	found := false
	for _, kv := range out {
		if kv == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %q in env, got %v", want, out)
	}
	// Original entries preserved.
	for _, kv := range parent {
		var hit bool
		for _, got := range out {
			if got == kv {
				hit = true
				break
			}
		}
		if !hit {
			t.Fatalf("parent entry %q missing from output env %v", kv, out)
		}
	}
}

func TestDoltServerEnv_RespectsUserOverride(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "DOLT_GC_SCHEDULER=LOADAVG", "HOME=/home/test"}
	out := doltServerEnv(parent)

	// User-provided value must be preserved exactly.
	count := 0
	for _, kv := range out {
		if kv == "DOLT_GC_SCHEDULER=LOADAVG" {
			count++
		}
		if kv == "DOLT_GC_SCHEDULER=NONE" {
			t.Fatalf("user override clobbered by default: %v", out)
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one DOLT_GC_SCHEDULER=LOADAVG entry, got %d in %v", count, out)
	}
}

func TestDoltServerEnv_RespectsEmptyUserValue(t *testing.T) {
	// An explicit empty value (DOLT_GC_SCHEDULER=) is still a user
	// override and we must not replace it.
	parent := []string{"DOLT_GC_SCHEDULER="}
	out := doltServerEnv(parent)
	for _, kv := range out {
		if kv == "DOLT_GC_SCHEDULER=NONE" {
			t.Fatalf("explicit empty-value override clobbered: %v", out)
		}
	}
}
