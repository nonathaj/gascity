package subprocess

import (
	"testing"

	"github.com/gastownhall/gascity/internal/testutil"
)

// TestMain enforces a hard lifetime bound on this test binary. This
// package spawns real provider subprocesses; a killed `go test` run
// orphans the binary on Windows (incident gw-qhs).
func TestMain(m *testing.M) {
	testutil.RunWithWatchdog(m)
}
