package herdr

import (
	"errors"
	"fmt"
	"os/exec"
	"sync/atomic"
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/runtime/runtimetest"
)

// TestHerdrConformance runs the full runtime.Provider conformance suite against
// the herdr provider backed by a real herdr binary. Each session gets its own
// isolated herdr session-server so the contract's session-scoped assertions
// (ListRunning, orphan detection, …) don't observe sibling sessions. Skipped
// when herdr is unavailable or in -short mode.
//
// The suite commands sessions with an arbitrary shell (`sleep`), which only
// registers under the legacy herdr ≤ 0.7.4 `agent start`. On herdr ≥ 0.7.5 the
// provider launches via pane verbs, whose registration is detection-driven, so
// an arbitrary shell never becomes an agent. Skip those starts instead of
// failing: the detection-driven path is covered by TestProviderLivePaneLaunch.
func TestHerdrConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live herdr conformance in -short mode")
	}
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr not installed")
	}

	var counter int64
	runtimetest.RunProviderTestsWithOptions(t, func(t *testing.T) (runtime.Provider, runtime.Config, string) {
		n := atomic.AddInt64(&counter, 1)
		p := New(fmt.Sprintf("gctest-conf-%d", n), t.TempDir(), t.TempDir(), 0)
		t.Cleanup(func() { _ = p.TeardownServer() })
		return p, runtime.Config{WorkDir: t.TempDir()}, fmt.Sprintf("conf-%d", n)
	}, runtimetest.Options{
		SkipStartError: func(err error) (string, bool) {
			if errors.Is(err, ErrAgentNotDetected) {
				return "pane-verb launch registers only herdr-detected agents; arbitrary shell not detectable", true
			}
			return "", false
		},
	})
}
