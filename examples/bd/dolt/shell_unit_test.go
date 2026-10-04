package dolt_test

import (
	"path/filepath"
	"testing"
)

// TestDoltShellUnitTests runs the deterministic stubbed shell unit tests under
// test/dolt. They are plain sh scripts that exit non-zero on a failed case,
// and no Makefile target, workflow or hook invokes them, so without this test
// they only ever ran by hand. Running them here puts them under
// `go test ./examples/bd/dolt/...`.
//
// latency_test.sh is deliberately not in the list: it asserts wall-clock
// bounds on a 50 ms sleep, and this test has no build tag, so one scheduler
// stall on a loaded runner would fail the untagged unit suite for an unrelated
// change. It still runs by hand.
func TestDoltShellUnitTests(t *testing.T) {
	testDir := filepath.Join(repoRoot(t), "..", "..", "..", "test", "dolt")
	for _, script := range []string{
		"advisory_dedup_test.sh",
		"compact_gain_drift_proof_test.sh",
		"compact_mixed_drift_proof_test.sh",
		"conn_max_test.sh",
	} {
		t.Run(script, func(t *testing.T) {
			out, err := newShScriptCmd(filepath.Join(testDir, script), filteredEnv()).CombinedOutput()
			if err != nil {
				t.Fatalf("sh %s failed: %v\n%s", script, err, out)
			}
			t.Logf("%s", out)
		})
	}
}
