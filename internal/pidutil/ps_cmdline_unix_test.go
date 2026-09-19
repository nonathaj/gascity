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

// TestPSCmdlineIsBounded mirrors the existing zombie-probe guard: a hung ps must
// not stall a caller that runs on a reconciler tick.
func TestPSCmdlineIsBounded(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "ps"), []byte("#!/bin/sh\nexec sleep 10\n"), 0o755); err != nil {
		t.Fatalf("WriteFile(ps): %v", err)
	}
	t.Setenv("PATH", strings.Join([]string{binDir, os.Getenv("PATH")}, string(os.PathListSeparator)))
	t.Setenv("GC_PIDUTIL_PS_TIMEOUT", "1s")

	start := time.Now()
	_, _ = psCmdline(os.Getpid())
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("psCmdline took %s, want a bounded timeout", elapsed)
	}
}

// TestPSCmdlineParsesOwnArgv exercises the ps parse path directly. Calling
// psCmdline bypasses Cmdline's /proc shortcut, so the parser this PR adds
// gets real coverage on linux runners too — otherwise it runs nowhere in CI.
func TestPSCmdlineParsesOwnArgv(t *testing.T) {
	argv, err := psCmdline(os.Getpid())
	if err != nil {
		t.Fatalf("psCmdline(self) on %s: %v", runtime.GOOS, err)
	}
	if len(argv) == 0 || !strings.Contains(filepath.Base(argv[0]), "pidutil") {
		t.Fatalf("psCmdline(self) = %q, want test binary argv", argv)
	}
}
