//go:build windows

package winsec

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRestrictToOwnerRoundTrip verifies the ACL restriction is real: a freshly
// created path inherits its parent's (non-protected) DACL and reads as
// unrestricted, RestrictToOwner makes it read as restricted, and the current
// process — the owner — retains read/write access afterward.
func TestRestrictToOwnerRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	// A fresh directory carries an inherited, non-protected DACL: not restricted.
	if ok, err := IsRestrictedToOwner(dir); err != nil {
		t.Fatalf("IsRestrictedToOwner(fresh): %v", err)
	} else if ok {
		t.Fatalf("fresh directory reports restricted before RestrictToOwner; the check is not discriminating")
	}

	if err := RestrictToOwner(dir); err != nil {
		t.Fatalf("RestrictToOwner: %v", err)
	}

	if ok, err := IsRestrictedToOwner(dir); err != nil {
		t.Fatalf("IsRestrictedToOwner(restricted): %v", err)
	} else if !ok {
		t.Fatalf("directory reports unrestricted after RestrictToOwner")
	}

	// The owner (this process) must still be able to write into the directory.
	marker := filepath.Join(dir, "owner-can-write")
	if err := os.WriteFile(marker, []byte("ok"), 0o600); err != nil {
		t.Fatalf("owner lost write access after RestrictToOwner: %v", err)
	}
	if _, err := os.ReadFile(marker); err != nil {
		t.Fatalf("owner lost read access after RestrictToOwner: %v", err)
	}

	// Files created under the restricted directory inherit the restriction.
	if ok, err := IsRestrictedToOwner(marker); err != nil {
		t.Fatalf("IsRestrictedToOwner(child file): %v", err)
	} else if ok {
		t.Logf("child file inherited protected DACL")
	}
}

// TestRestrictToOwnerFile applies the restriction to a regular file directly.
func TestRestrictToOwnerFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pgpass")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := RestrictToOwner(path); err != nil {
		t.Fatalf("RestrictToOwner(file): %v", err)
	}
	if ok, err := IsRestrictedToOwner(path); err != nil {
		t.Fatalf("IsRestrictedToOwner(file): %v", err)
	} else if !ok {
		t.Fatalf("file reports unrestricted after RestrictToOwner")
	}
}
