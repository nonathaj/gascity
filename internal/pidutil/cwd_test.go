package pidutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestCwdReadsOwnWorkingDirectory is the baseline: a process can always read
// its own working directory, so Cwd must agree with os.Getwd for self.
func TestCwdReadsOwnWorkingDirectory(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	got, err := Cwd(os.Getpid())
	if err != nil {
		if !Supported() {
			t.Skipf("Cwd unsupported on %s: %v", runtime.GOOS, err)
		}
		t.Fatalf("Cwd(self): %v", err)
	}
	if !sameDir(t, got, want) {
		t.Fatalf("Cwd(self) = %q, want %q", got, want)
	}
}

// TestCwdReadsAnotherProcessWorkingDirectory is the case the managed-dolt
// ownership probe actually needs (gw-4np): identifying the working directory of
// a process this one did not exec into. It is the whole point of the API --
// reading self proves nothing about the cross-process path, which on Windows
// walks the target's PEB rather than a local syscall.
func TestCwdReadsAnotherProcessWorkingDirectory(t *testing.T) {
	if !Supported() {
		t.Skipf("Cwd unsupported on %s", runtime.GOOS)
	}
	dir := t.TempDir()

	cmd := sleeperInDir(t, dir)
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// The child needs to be far enough along that its PEB is populated.
	var (
		got string
		err error
	)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got, err = Cwd(cmd.Process.Pid); err == nil && strings.TrimSpace(got) != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Cwd(child): %v", err)
	}
	if !sameDir(t, got, dir) {
		t.Fatalf("Cwd(child) = %q, want %q", got, dir)
	}
}

// TestCwdRejectsInvalidPID keeps the probe fail-closed. Ownership checks treat a
// successful read as evidence, so a bogus PID must error rather than return an
// empty string a caller might compare loosely.
func TestCwdRejectsInvalidPID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if got, err := Cwd(pid); err == nil {
			t.Fatalf("Cwd(%d) = %q, want an error", pid, got)
		}
	}
}

// sleeperInDir starts a long-lived child whose working directory is dir, using
// only tools guaranteed present on the platform.
func sleeperInDir(t *testing.T, dir string) *exec.Cmd {
	t.Helper()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// timeout.exe needs a console; ping against loopback is the standard
		// console-free sleep and keeps the process alive without a shell.
		cmd = exec.Command("ping", "-n", "60", "127.0.0.1")
	} else {
		cmd = exec.Command("sleep", "60")
	}
	cmd.Dir = dir
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleeper in %s: %v", dir, err)
	}
	return cmd
}

// sameDir compares two directory paths through EvalSymlinks so a temp dir
// reported via its canonical form (macOS /private/var, Windows 8.3 short names)
// still matches the path the test handed out.
func sameDir(t *testing.T, a, b string) bool {
	t.Helper()
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Clean(r)
		}
		return filepath.Clean(p)
	}
	return strings.EqualFold(resolve(a), resolve(b))
}
