package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gastownhall/gascity/internal/winsec"
)

// assertOwnerRestricted asserts path is restricted to its owner using the
// platform's native mechanism: Unix mode bits, or a protected owner-only DACL
// on Windows (os.Chmod cannot revoke access there, so enforceGCPermissions
// applies an ACL instead — see internal/winsec).
func assertOwnerRestricted(t *testing.T, path string, unixPerm os.FileMode) {
	t.Helper()
	if runtime.GOOS == "windows" {
		ok, err := winsec.IsRestrictedToOwner(path)
		if err != nil {
			t.Fatalf("IsRestrictedToOwner(%q): %v", path, err)
		}
		if !ok {
			t.Errorf("%q is not restricted to owner (expected a protected owner-only DACL)", path)
		}
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != unixPerm {
		t.Errorf("%q perm = %o, want %o", path, perm, unixPerm)
	}
}

func TestEnforceGCPermissions_TightensLooseDir(t *testing.T) {
	cityPath := t.TempDir()
	gcDir := filepath.Join(cityPath, ".gc")
	secretsPath := filepath.Join(gcDir, secretsDir)

	// Create with loose permissions (as gc init currently does).
	if err := os.MkdirAll(secretsPath, 0o755); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	enforceGCPermissions(cityPath, &stderr)
	if stderr.Len() > 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}

	assertOwnerRestricted(t, gcDir, gcDirPerm)
	assertOwnerRestricted(t, secretsPath, secretsDirPerm)
}

func TestEnforceGCPermissions_NoErrorWhenMissing(t *testing.T) {
	cityPath := t.TempDir()
	var stderr bytes.Buffer
	// Should not error when .gc/ doesn't exist.
	enforceGCPermissions(cityPath, &stderr)
	if stderr.Len() > 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}
}

func TestEnforceGCPermissions_AlreadyCorrect(t *testing.T) {
	cityPath := t.TempDir()
	gcDir := filepath.Join(cityPath, ".gc")
	if err := os.MkdirAll(gcDir, gcDirPerm); err != nil {
		t.Fatal(err)
	}

	var stderr bytes.Buffer
	enforceGCPermissions(cityPath, &stderr)
	if stderr.Len() > 0 {
		t.Errorf("unexpected stderr: %s", stderr.String())
	}

	assertOwnerRestricted(t, gcDir, gcDirPerm)
}
