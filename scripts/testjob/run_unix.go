//go:build !windows

package main

import "runtime"

// run proxies the command unwrapped: on non-Windows hosts containment
// is owned by the systemd slice enrollment in scripts/lib/test-slice.sh.
func run(args []string) int {
	return proxy(args)
}

// jobCount falls back to a CPU-derived recommendation; memory budgeting
// on these hosts is owned by scripts/test-local-job-count.
func jobCount() int {
	return recommendedJobs(runtime.NumCPU(), 0)
}
