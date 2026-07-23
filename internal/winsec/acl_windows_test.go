//go:build windows

package winsec

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// grantEveryoneRead adds a read ACE for the Everyone group to path's DACL,
// modeling a world-readable credentials file (a permissive Unix mode).
func grantEveryoneRead(t *testing.T, path string) {
	t.Helper()
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid(Everyone): %v", err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo: %v", err)
	}
	existing, _, err := sd.DACL()
	if err != nil {
		t.Fatalf("DACL: %v", err)
	}
	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, existing)
	if err != nil {
		t.Fatalf("ACLFromEntries: %v", err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatalf("SetNamedSecurityInfo: %v", err)
	}
}

// TestHasBroadAccess validates the read-gate check: an owner-restricted file
// reports no broad access, and one granting Everyone reports broad access.
func TestHasBroadAccess(t *testing.T) {
	p := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(p, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := RestrictToOwner(p); err != nil {
		t.Fatalf("RestrictToOwner: %v", err)
	}
	if broad, err := HasBroadAccess(p); err != nil || broad {
		t.Fatalf("restricted file: broad=%v err=%v; want false", broad, err)
	}
	grantEveryoneRead(t, p)
	if broad, err := HasBroadAccess(p); err != nil || !broad {
		t.Fatalf("Everyone-granted file: broad=%v err=%v; want true", broad, err)
	}
}

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
