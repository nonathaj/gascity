// Package testutil contains helpers shared by tests across platforms.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/pathutil"
)

// CanonicalPath returns the production path-normalized form used for
// comparisons. This keeps tests stable on macOS where /tmp and /var can be
// reported through /private aliases.
func CanonicalPath(path string) string {
	return pathutil.NormalizePathForCompare(path)
}

// AssertSamePath compares two filesystem paths after canonicalization.
func AssertSamePath(t *testing.T, got, want string) {
	t.Helper()
	got = CanonicalPath(got)
	want = CanonicalPath(want)
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

// NativePIDForShellPID maps a pid produced by a POSIX shell to the pid the OS process
// APIs use, and reports whether the mapping succeeded.
//
// Under Git for Windows, "$$" and "$!" are MSYS pids — a different numbering space from
// the native pids os.FindProcess, OpenProcess and taskkill operate on. A fixture that
// writes a shell pid to a file and later hands it to a Go kill/liveness call is
// therefore acting on an unrelated number. That is not merely a broken assertion: it can
// TERMINATE AN UNRELATED PROCESS on the developer's machine when the number happens to
// collide with a live native pid, and observed MSYS values (1749, 10782, 46077) sit
// squarely in the range Windows assigns.
//
// ps -W lists native processes with the MSYS pid in column 1 and the native pid in
// column 4. Off Windows there is one pid space, so this is identity and always ok=true.
//
// ok=false means the mapping could not be established — callers must NOT fall back to
// using the raw value, because that is the dangerous case.
func NativePIDForShellPID(shellPID int) (int, bool) {
	if runtime.GOOS != "windows" {
		return shellPID, shellPID > 0
	}
	if shellPID <= 0 {
		return 0, false
	}
	out, err := exec.Command("sh", "-c",
		"ps -W 2>/dev/null | awk -v p="+strconv.Itoa(shellPID)+" 'NR>1 && $1==p {print $4; exit}'").Output()
	if err != nil {
		return 0, false
	}
	native, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil || native <= 0 {
		return 0, false
	}
	return native, true
}

// SetTestHome pins the test's home directory on every platform. os.UserHomeDir
// reads HOME on Unix but USERPROFILE on Windows (doctrine T1), so a test that
// sets only HOME still resolves ~ to the developer's real profile there — it then
// reads and writes the user's actual data instead of the fixture, and any
// assertion about "the home dir" silently tests the wrong tree.
func SetTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// AbsFixture turns a rootless POSIX fixture path ("/data/projects/app") into a
// genuinely absolute path on every platform. On Windows "/data/projects/app" has
// no volume and is therefore NOT absolute, so production code that calls
// filepath.Abs on it silently prepends the current drive ("D:\data\projects\app")
// — changing any value derived from it, such as a project slug, and making the
// test compare against a path production never produces (doctrine T2).
//
// Packages with their own local absFixture predate this; prefer this one in new
// tests.
func AbsFixture(p string) string {
	if runtime.GOOS == "windows" {
		return filepath.FromSlash("C:" + p)
	}
	return p
}

// ShortTempDir returns a test-owned temporary directory rooted at a short path
// on macOS so Unix socket paths stay under the platform limit.
func ShortTempDir(t *testing.T, prefix string) string {
	t.Helper()
	root := os.TempDir()
	switch runtime.GOOS {
	case "darwin":
		root = "/tmp"
	case "windows":
		// os.TempDir here is the deep per-package test root (TestMain redirects
		// TMP), which pushes AF_UNIX socket paths past the sun_path limit and
		// hosted runners have no \tmp. LOCALAPPDATA is short (~38 chars) and
		// always present.
		if lad := os.Getenv("LOCALAPPDATA"); lad != "" {
			short := filepath.Join(lad, "gct")
			if err := os.MkdirAll(short, 0o700); err == nil {
				root = short
			}
		}
	}
	dir, err := os.MkdirTemp(root, prefix)
	if err != nil {
		t.Fatalf("MkdirTemp(%q, %q): %v", root, prefix, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
