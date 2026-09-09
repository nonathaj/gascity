package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Focused proof command for this file:
//
//	go test ./internal/config/ -run TestRoutedTierJQFailure -v
//
// Review finding R6. The routed tier pipes bd's rows through jq to apply the
// shared liveness predicate. That jq can fail — a malformed row, a non-string
// .status that makes ascii_downcase throw, jq missing from PATH — and when it
// did, its stderr went to /dev/null and its exit status was discarded. The tier
// variable came back empty, which is indistinguishable from "no routed work",
// so the query degraded to the legacy tiers with no signal at all.
//
// That is the project's "Don't Swallow Errors" rule, and it is also this
// build's own theme: a silent no-op on the claim path is exactly what hid the
// original defect for hours.

// runShellCapturingStderr runs shellCmd with a fake `bd` on PATH and returns
// stdout and stderr separately. runShellWithFakeBd uses cmd.Output() and so
// cannot see the diagnostic this test is about.
func runShellCapturingStderr(t *testing.T, shellCmd, bdScript string) (string, string) {
	t.Helper()

	tmp := t.TempDir()
	bdPath := filepath.Join(tmp, "bd")
	if err := os.WriteFile(bdPath, []byte(bdScript), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	var stdout, stderr bytes.Buffer
	cmd := testShellCommand(shellCmd, tmp)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A non-zero exit is a legitimate outcome here; the streams are the subject.
	_ = cmd.Run()
	return stdout.String(), stderr.String()
}

// fakeBdReadyNonStringStatus serves a routed-ready row whose .status is a JSON
// number. `(.status // "") | ascii_downcase` throws on it, so the liveness jq
// exits non-zero having printed nothing.
func fakeBdReadyNonStringStatus() string {
	return fakeBdReady(`[{"id":"fo-bad","status":123}]`)
}

// TestRoutedTierJQFailureIsReported is the R6 regression: a failing liveness
// filter must announce itself rather than masquerading as an empty tier.
func TestRoutedTierJQFailureIsReported(t *testing.T) {
	a := livenessTestAgent()

	_, stderr := runShellCapturingStderr(t, a.EffectiveWorkQuery(), fakeBdReadyNonStringStatus())

	if strings.TrimSpace(stderr) == "" {
		t.Errorf("liveness jq failed and the work query said nothing on stderr; " +
			"a failed filter is indistinguishable from an empty tier, which is the " +
			"silent-degradation shape this build exists to remove")
	}
}

// TestRoutedTierJQFailureStaysNonFatal pins the other half: reporting the
// failure must not change the tier's behavior. Falling through to the legacy
// tiers is the correct response to a broken filter — the query still has to
// answer, and a work query that dies takes the worker with it.
func TestRoutedTierJQFailureStaysNonFatal(t *testing.T) {
	a := livenessTestAgent()

	stdout, _ := runShellCapturingStderr(t, a.EffectiveWorkQuery(), fakeBdReadyNonStringStatus())

	if got := strings.TrimSpace(stdout); got != "[]" {
		t.Errorf("work query stdout = %q, want %q: a failed liveness filter must "+
			"serve no work, not malformed work", got, "[]")
	}
}

// TestRoutedTierHealthyPathStaysQuiet keeps the diagnostic honest. A tier that
// legitimately matches nothing is the overwhelmingly common case and must not
// print anything, or the signal is noise within a day.
func TestRoutedTierHealthyPathStaysQuiet(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ready string
	}{
		{name: "no_rows", ready: `[]`},
		{name: "all_closed", ready: `[{"id":"fo-closed","status":"closed"}]`},
		{name: "live_row", ready: `[{"id":"fo-open","status":"open"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := livenessTestAgent()

			_, stderr := runShellCapturingStderr(t, a.EffectiveWorkQuery(), fakeBdReady(tc.ready))

			if strings.TrimSpace(stderr) != "" {
				t.Errorf("healthy work query wrote to stderr: %q", stderr)
			}
		})
	}
}
