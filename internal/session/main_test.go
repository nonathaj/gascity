package session

import (
	"testing"

	"github.com/gastownhall/gascity/internal/testutil"
)

// TestMain enforces a hard lifetime bound on this test binary. This
// package spawns fake submit pollers and other subprocesses; on Windows
// a killed `go test` run orphans the binary mid cmd.Wait, and only a
// watchdog os.Exit reliably ends it (incident gw-qhs).
func TestMain(m *testing.M) {
	testutil.RunWithWatchdog(m)
}
