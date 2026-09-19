// The ps-based fallbacks are Unix-only: psStartTime and psCmdline shell out to
// ps(1), which Windows has no equivalent of, and they live in pidutil_unix.go.
// The rest of the portable suites stay platform-neutral so they keep running on
// the Windows gate (internal/pidutil is on .github/windows-test-packages.txt).
//go:build !windows

package pidutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestPSStartTimeReturnsIdentity covers the new fallback's success path.
// ps -o lstart= works on linux too, so this runs on every platform — without
// it, no CI job ever executes a successful psStartTime.
func TestPSStartTimeReturnsIdentity(t *testing.T) {
	got, err := psStartTime(os.Getpid())
	if err != nil {
		t.Fatalf("psStartTime(self) on %s: %v", runtime.GOOS, err)
	}
	if strings.TrimSpace(got) == "" {
		t.Fatalf("psStartTime(self) on %s returned an empty identity", runtime.GOOS)
	}
}

// TestPSStartTimeIsBounded mirrors the other ps probes in this package: callers
// sit in a post-SIGKILL reap loop, so a hung ps must not stall them.
func TestPSStartTimeIsBounded(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "ps"), []byte("#!/bin/sh\nexec sleep 10\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(ps): %v", err)
	}
	t.Setenv("PATH", strings.Join([]string{binDir, os.Getenv("PATH")}, string(os.PathListSeparator)))
	t.Setenv("GC_PIDUTIL_PS_TIMEOUT", "1s")

	start := time.Now()
	_, _ = psStartTime(os.Getpid())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("psStartTime took %s, want a bounded timeout", elapsed)
	}
}
