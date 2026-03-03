package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func healthyOps() doltHealthOps {
	return doltHealthOps{
		hostPort:       "127.0.0.1:3306",
		dialTCP:        func(_ string) error { return nil },
		queryProbe:     func() error { return nil },
		writeProbe:     func() error { return nil },
		isUnhealthy:    func() (bool, string) { return false, "" },
		setUnhealthy:   func(_ string) {},
		clearUnhealthy: func() {},
		recover:        func() error { return nil },
	}
}

func TestBdHealthCheckAllHealthy(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("bd", false, healthyOps(),
		nil, false, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0; stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"TCP", "ok", "Query", "Write"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q: %s", want, out)
		}
	}
}

func TestBdHealthCheckAllHealthyQuiet(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("bd", false, healthyOps(),
		nil, true, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if stdout.Len() != 0 {
		t.Errorf("quiet mode should produce no stdout, got: %s", stdout.String())
	}
}

func TestBdHealthCheckTCPFail(t *testing.T) {
	ops := healthyOps()
	ops.dialTCP = func(_ string) error { return fmt.Errorf("connection refused") }
	recovered := false
	ops.recover = func() error { recovered = true; return nil }

	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("bd", false, ops, nil, false, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (recovery succeeds)", code)
	}
	if !recovered {
		t.Error("recovery should have been attempted")
	}
	out := stdout.String()
	if !strings.Contains(out, "FAIL") {
		t.Errorf("should show TCP FAIL: %s", out)
	}
	if !strings.Contains(out, "Attempting recovery") {
		t.Errorf("should show recovery attempt: %s", out)
	}
	// Query and write should not run when TCP fails.
	if strings.Contains(out, "Query") {
		t.Errorf("query should not run when TCP fails: %s", out)
	}
}

func TestBdHealthCheckQueryFail(t *testing.T) {
	ops := healthyOps()
	ops.queryProbe = func() error { return fmt.Errorf("query timeout") }
	ops.recover = func() error { return nil }

	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("bd", false, ops, nil, false, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (recovery succeeds)", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "Query (SELECT 1): FAIL") {
		t.Errorf("should show query FAIL: %s", out)
	}
	// Write should not run when query fails.
	if strings.Contains(out, "Write") {
		t.Errorf("write should not run when query fails: %s", out)
	}
}

func TestBdHealthCheckWriteFail(t *testing.T) {
	ops := healthyOps()
	ops.writeProbe = func() error { return fmt.Errorf("write error") }
	ops.recover = func() error { return nil }

	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("bd", false, ops, nil, false, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (recovery succeeds)", code)
	}
	out := stdout.String()
	if !strings.Contains(out, "Write: FAIL") {
		t.Errorf("should show write FAIL: %s", out)
	}
}

func TestBdHealthCheckRecoveryFails(t *testing.T) {
	ops := healthyOps()
	ops.dialTCP = func(_ string) error { return fmt.Errorf("refused") }
	ops.recover = func() error { return fmt.Errorf("recovery failed") }

	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("bd", false, ops, nil, false, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (recovery fails)", code)
	}
	if !strings.Contains(stderr.String(), "recovery failed") {
		t.Errorf("stderr should mention recovery failure: %s", stderr.String())
	}
}

func TestBdHealthCheckUnhealthySignal(t *testing.T) {
	ops := healthyOps()
	ops.isUnhealthy = func() (bool, string) { return true, "previous failure" }

	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("bd", false, ops, nil, false, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "DOLT_UNHEALTHY: previous failure") {
		t.Errorf("should report unhealthy signal: %s", stdout.String())
	}
}

func TestBdHealthCheckFileProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("file", false, doltHealthOps{},
		func() error { return nil }, false, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Beads provider: healthy") {
		t.Errorf("should show healthy message: %s", stdout.String())
	}
}

func TestBdHealthCheckFileProviderError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("file", false, doltHealthOps{},
		func() error { return fmt.Errorf("file error") }, false, &stdout, &stderr)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "file error") {
		t.Errorf("stderr should mention error: %s", stderr.String())
	}
}

func TestBdHealthCheckDoltSkip(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("bd", true, doltHealthOps{},
		func() error { return nil }, false, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Beads provider: healthy") {
		t.Errorf("GC_DOLT=skip should skip bd checks: %s", stdout.String())
	}
}

func TestBdHealthCheckExecProvider(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := doBdHealthCheck("exec:/usr/bin/my-beads", false, doltHealthOps{},
		func() error { return nil }, false, &stdout, &stderr)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}
