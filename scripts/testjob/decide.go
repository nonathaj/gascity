package main

// Decision logic for the Windows test-containment wrapper — the
// analogue of gc_test_slice_should_wrap in scripts/lib/test-slice.sh
// (see engdocs/design/windows-systemd-parity.md, D2). Pure functions so
// the matrix is unit-testable on every platform.

const (
	// testJobName is the shared named Job Object all wrapped test runs
	// join, the analogue of gascity-test.slice. Kill-on-close fires
	// when the LAST wrapper's handle closes, so concurrent runs compose.
	testJobName = "gascity-test"
	// optOutEnv is the same opt-out the Linux slice enrollment honors.
	optOutEnv = "GC_TEST_NO_SLICE"
	// jobMemoryEnv overrides the job's committed-memory cap, in bytes.
	jobMemoryEnv = "GC_TEST_JOB_MEMORY"
	// shardMemoryBytes is the per-shard budget used for -count: a
	// fast-suite shard builds ~2.8 GiB of test binaries (mirrors
	// scripts/test-local-job-count).
	shardMemoryBytes = 4 << 30
	// maxAutoJobs caps -count regardless of host size (mirrors
	// scripts/test-local-job-count).
	maxAutoJobs = 16
)

// shouldWrap reports whether this invocation creates/joins the
// containment job: only on Windows, not when explicitly opted out, and
// not when already inside the job (the nested-runner guard — the
// /proc/self/cgroup check's analogue).
func shouldWrap(goos string, optedOut, alreadyInJob bool) bool {
	if goos != "windows" {
		return false
	}
	if optedOut {
		return false
	}
	return !alreadyInJob
}

// recommendedJobs sizes shard parallelism to the tighter of CPU count
// and the memory budget, capped: min(cpus, mem/4GiB, 16), at least 1.
// A zero budget means "unknown" and constrains nothing.
func recommendedJobs(cpus int, memBytes uint64) int {
	jobs := cpus
	if jobs < 1 {
		jobs = 1
	}
	if memBytes > 0 {
		byMem := int(memBytes / shardMemoryBytes)
		if byMem < 1 {
			byMem = 1
		}
		if byMem < jobs {
			jobs = byMem
		}
	}
	if jobs > maxAutoJobs {
		jobs = maxAutoJobs
	}
	return jobs
}

// defaultJobMemory derives the job cap from available physical memory:
// 75%, leaving headroom for the rest of the host.
func defaultJobMemory(availBytes uint64) uint64 {
	return availBytes / 4 * 3
}
