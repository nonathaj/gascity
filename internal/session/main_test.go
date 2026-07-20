package session

import (
	"fmt"
	"os"
	"testing"

	"github.com/gastownhall/gascity/internal/testutil"
)

// TestMain enforces a hard lifetime bound on this test binary. This
// package spawns fake submit pollers and other subprocesses; on Windows
// a killed `go test` run orphans the binary mid cmd.Wait, and only a
// watchdog os.Exit reliably ends it (incident gw-qhs).
//
// The argv check is the fork-bomb backstop: the submit path spawns "the
// gc binary" as `<exe> nudge poll ...`, and inside tests that resolves
// to THIS binary. isGoTestExecutable refuses the spawn, but if any
// regression ever lets it through again, running the suite here would
// re-detonate incident gw-8g5 (4,500 processes in ~10 minutes). A test
// binary never legitimately receives positional args, so dispatch — die
// loudly, never run the suite.
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == "nudge" && os.Args[2] == "poll" {
		fmt.Fprintln(os.Stderr, "session.test: refusing to run as a nudge poller (re-exec guard, incident gw-8g5)")
		os.Exit(1)
	}
	testutil.RunWithWatchdog(m)
}
