package main

import (
	"os"

	"github.com/gastownhall/gascity/internal/execshim"
)

// isTestBinary reports whether the current process is a Go test binary
// (".test" on Unix, ".test.exe" on Windows — doctrine P6). Used by
// runtime guards to keep tests from hitting host infrastructure.
func isTestBinary() bool {
	if len(os.Args) == 0 {
		return false
	}
	return execshim.IsGoTestExecutable(os.Args[0])
}

// productMetricsChildEnvSpyPathEnv marks an intentional, contained test
// self-spawn: cmd/gc's TestMain intercepts a child carrying this env var
// and exits after writing its environment snapshot, so the spawned test
// binary can never re-run the suite. The self-spawn refusal guards honor
// it so the product-metrics child-env tests can exercise the real spawn
// paths.
const productMetricsChildEnvSpyPathEnv = "GC_TEST_PRODUCT_METRICS_DIRECT_CHILD_ENV_SPY_PATH"

// refuseTestBinarySelfSpawn reports whether spawning path would re-enter
// this test suite: gc self-spawn sites (nudge poller, supervisor start,
// sling, perf) resolve os.Executable, which inside tests is the test
// binary, and running it re-runs the whole suite per spawn — a fork
// bomb (incident gw-8g5: 4,500 processes, ~246 GB commit in ~10
// minutes). Spawns defused by the TestMain spy intercept are allowed.
func refuseTestBinarySelfSpawn(path string) bool {
	return execshim.IsGoTestExecutable(path) &&
		os.Getenv(productMetricsChildEnvSpyPathEnv) == ""
}
