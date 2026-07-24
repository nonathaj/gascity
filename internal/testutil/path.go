// Package testutil contains helpers shared by tests across platforms.
package testutil

import (
	"os"
	"path/filepath"
	"runtime"
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
