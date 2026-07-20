//go:build windows

package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"

	"github.com/gastownhall/gascity/internal/winjob"
)

// run wraps the command in the shared kill-on-close test job when the
// decision matrix says to, then proxies it. Containment failures warn
// and fall back to a plain run — matching the Linux enrollment's
// best-effort contract — because a missed wrap must never block a test
// run outright.
func run(args []string) int {
	if job := enterTestJob(); job != nil {
		defer job.Close() //nolint:errcheck // close is the containment trigger
	}
	return proxy(args)
}

// enterTestJob creates/joins the shared job and assigns this process,
// so the proxied child (and its whole tree) inherits membership.
// Returns nil when wrapping is skipped or fails.
func enterTestJob() *winjob.Job {
	optedOut := os.Getenv(optOutEnv) == "1"
	alreadyIn, err := winjob.InJob(testJobName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testjob: membership probe failed, running unwrapped: %v\n", err)
		return nil
	}
	if !shouldWrap(runtime.GOOS, optedOut, alreadyIn) {
		return nil
	}
	job, err := winjob.Create(testJobName, winjob.Limits{
		KillOnClose: true,
		JobMemory:   jobMemoryCap(),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "testjob: creating job, running unwrapped: %v\n", err)
		return nil
	}
	if err := job.AssignCurrent(); err != nil {
		fmt.Fprintf(os.Stderr, "testjob: joining job, running unwrapped: %v\n", err)
		_ = job.Close()
		return nil
	}
	return job
}

// jobMemoryCap resolves the job's committed-memory cap:
// GC_TEST_JOB_MEMORY (bytes) wins, else 75% of available physical
// memory, else no cap.
func jobMemoryCap() uint64 {
	if raw := os.Getenv(jobMemoryEnv); raw != "" {
		if v, err := strconv.ParseUint(raw, 10, 64); err == nil {
			return v
		}
		fmt.Fprintf(os.Stderr, "testjob: ignoring unparsable %s=%q\n", jobMemoryEnv, raw)
	}
	avail, err := winjob.AvailablePhysicalMemory()
	if err != nil {
		return 0
	}
	return defaultJobMemory(avail)
}

// jobCount reports recommended shard parallelism. Inside the job the
// budget is the job's own memory cap; outside it is the would-be
// default cap.
func jobCount() int {
	var budget uint64
	if in, err := winjob.InJob(testJobName); err == nil && in {
		if job, err := winjob.Create(testJobName, winjob.Limits{}); err == nil {
			if limit, _, err := job.MemoryBudget(); err == nil {
				budget = limit
			}
			_ = job.Close()
		}
	}
	if budget == 0 {
		budget = jobMemoryCap()
	}
	return recommendedJobs(runtime.NumCPU(), budget)
}
