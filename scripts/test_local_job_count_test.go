package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runJobCount runs scripts/test-local-job-count with a synthetic /proc/meminfo
// and a fixed CPU count, so the result depends only on the memory probe.
func runJobCount(t *testing.T, meminfo string, extraEnv ...string) string {
	t.Helper()
	repoRoot := repoRoot(t)

	env := append(os.Environ(), "GC_TEST_LOCAL_CPUS=32")
	if meminfo != "" {
		path := filepath.Join(t.TempDir(), "meminfo")
		if err := os.WriteFile(path, []byte(meminfo), 0o600); err != nil {
			t.Fatalf("write meminfo fixture: %v", err)
		}
		env = append(env, "GC_TEST_LOCAL_MEMINFO="+path)
	}
	env = append(env, extraEnv...)

	cmd := testCommand("bash", filepath.Join(repoRoot, "scripts", "test-local-job-count"))
	cmd.Dir = repoRoot
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("test-local-job-count: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestJobCountPrefersMemAvailableOverMemFree pins the Linux reading: when the
// kernel reports MemAvailable, that value alone decides the budget. MemFree is
// deliberately much smaller here, so a regression that consulted it -- or that
// preferred the larger of the two -- would change the answer.
func TestJobCountPrefersMemAvailableOverMemFree(t *testing.T) {
	got := runJobCount(t, "MemTotal:      100000000 kB\nMemFree:         4194304 kB\nMemAvailable:   41943040 kB\n")
	if want := "10"; got != want {
		t.Fatalf("job count = %s, want %s (40 GiB MemAvailable / 4 GiB per job)", got, want)
	}
}

// TestJobCountFallsBackToMemFree covers Git for Windows, whose MSYS layer
// emulates /proc/meminfo with MemTotal and MemFree but no MemAvailable, and
// equally any pre-3.14 Linux kernel. Without the fallback the file parsed to
// nothing and detection reported "unknown", pinning a large machine to the
// three-job safety default.
func TestJobCountFallsBackToMemFree(t *testing.T) {
	got := runJobCount(t, "MemTotal:      100000000 kB\nMemFree:        41943040 kB\n")
	if want := "10"; got != want {
		t.Fatalf("job count = %s, want %s (40 GiB MemFree / 4 GiB per job)", got, want)
	}
}

// TestJobCountKeepsSafetyDefaultWhenMemoryIsUnknowable guards the other
// direction: a meminfo carrying neither field must not be read as "plenty of
// memory". The conservative three-job default still applies.
func TestJobCountKeepsSafetyDefaultWhenMemoryIsUnknowable(t *testing.T) {
	got := runJobCount(t, "MemTotal:      100000000 kB\n")
	if want := "3"; got != want {
		t.Fatalf("job count = %s, want the %s-job unknown-memory default", got, want)
	}
}

// TestJobCountHonorsExplicitMemoryOverride confirms an operator-supplied budget
// still wins over both probes, including the explicit "unknown" of zero.
func TestJobCountHonorsExplicitMemoryOverride(t *testing.T) {
	plenty := "MemTotal:      100000000 kB\nMemAvailable:   41943040 kB\n"
	if got, want := runJobCount(t, plenty, "GC_TEST_LOCAL_MEMORY_KIB=0"), "3"; got != want {
		t.Fatalf("job count with explicit unknown memory = %s, want %s", got, want)
	}
	if got, want := runJobCount(t, plenty, "GC_TEST_LOCAL_MEMORY_KIB=8388608"), "2"; got != want {
		t.Fatalf("job count with explicit 8 GiB = %s, want %s", got, want)
	}
}
