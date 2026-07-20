package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSliceEnrollFallbackMatrix runs the shell self-test for
// scripts/lib/test-slice.sh, which exercises gascity-test.slice
// auto-enrollment plus every plain-execution fallback (opt-out, missing
// systemd-run, unreachable user manager, missing slice unit, failing scope
// allocation, nested runners) with fake systemd-run/systemctl binaries on a
// fully controlled PATH.
func TestSliceEnrollFallbackMatrix(t *testing.T) {
	if runtime.GOOS == "windows" {
		// The self-test is a bash script exercising Linux user-systemd
		// enrollment; the slice machinery is inert on Windows (see
		// engdocs/design/windows-systemd-parity.md).
		t.Skip("gascity-test.slice enrollment is a Linux user-systemd mechanism")
	}
	root := repoRoot(t)

	cmd := exec.Command(filepath.Join(root, "scripts", "test-slice-enroll-test"))
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + t.TempDir(),
		"TMPDIR=" + t.TempDir(),
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test-slice-enroll-test failed: %v\n%s", err, out)
	}
}
