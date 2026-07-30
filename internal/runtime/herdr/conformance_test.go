package herdr

import (
	"fmt"
	"os/exec"
	goruntime "runtime"
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
func TestHerdrConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live herdr conformance in -short mode")
	}
	if goruntime.GOOS == "windows" {
		// gc dials net.Dial("unix", socketPath) to reach the session server, but
		// herdr on Windows does not create an AF_UNIX socket there: "herdr.sock"
		// is a regular file holding a descriptor for some other transport. The
		// server itself runs fine on Windows; gc simply cannot speak to it yet,
		// so this exercises an unimplemented path rather than a regression.
		// Dissolved by gw-j95, which teaches the client herdr's Windows transport.
		t.Skip("herdr provider has no Windows transport yet (gw-j95)")
	}
	if _, err := exec.LookPath("herdr"); err != nil {
		t.Skip("herdr not installed")
	}

	var counter int64
	runtimetest.RunProviderTests(t, func(t *testing.T) (runtime.Provider, runtime.Config, string) {
		n := atomic.AddInt64(&counter, 1)
		p := New(fmt.Sprintf("gctest-conf-%d", n), t.TempDir(), t.TempDir(), 0, 0)
		t.Cleanup(func() { _ = p.TeardownServer() })
		return p, runtime.Config{WorkDir: t.TempDir()}, fmt.Sprintf("conf-%d", n)
	})
}
