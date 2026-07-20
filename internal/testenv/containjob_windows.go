//go:build windows

package testenv

import (
	"fmt"
	"os"
	"strconv"

	"github.com/gastownhall/gascity/internal/winjob"
)

// testJob pins the containment job's handle for the life of the
// process: kill-on-close fires exactly when this process dies and the
// handle closes, taking every descendant with it.
var testJob *winjob.Job

// containTestProcessTree places the test binary in an anonymous
// kill-on-close Job Object — the structural analogue of the Linux
// gascity-test.slice enrollment, applied automatically inside every
// test binary via this package's lint-enforced blank import. Windows
// never tears down process trees, so without this a killed `go test`
// run orphans everything it spawned (incident gw-qhs) and a self-spawn
// bug amplifies unbounded (incident gw-8g5). Anonymous (per-binary)
// rather than shared: one binary's death reaps exactly its own tree.
// Best-effort: containment failure warns and never blocks a test run.
// Descendants deliberately cannot break away (no BREAKAWAY_OK) —
// gc's startDetached falls back to a contained spawn when denied.
func containTestProcessTree() {
	// Same explicit opt-out the Linux slice enrollment honors — for
	// debugging runs that deliberately leave a child alive.
	if os.Getenv("GC_TEST_NO_SLICE") == "1" {
		return
	}
	limits := winjob.Limits{KillOnClose: true}
	// Optional committed-memory cap for the whole test tree, in bytes.
	if raw := os.Getenv("GC_TEST_JOB_MEMORY"); raw != "" {
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil {
			limits.JobMemory = v
		} else {
			fmt.Fprintf(os.Stderr, "testenv: ignoring unparsable GC_TEST_JOB_MEMORY=%q\n", raw)
		}
	}
	job, err := winjob.Create("", limits)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testenv: test-tree containment unavailable (create): %v\n", err)
		return
	}
	if err := job.AssignCurrent(); err != nil {
		fmt.Fprintf(os.Stderr, "testenv: test-tree containment unavailable (assign): %v\n", err)
		_ = job.Close()
		return
	}
	testJob = job
}
