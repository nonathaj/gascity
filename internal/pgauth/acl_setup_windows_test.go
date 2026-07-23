//go:build windows

package pgauth

import (
	"os"
	"testing"

	"github.com/gastownhall/gascity/internal/winsec"
	"golang.org/x/sys/windows"
)

// applyUnixModeAsWindowsACL mirrors the intent of a Unix mode onto path's NTFS
// ACL so the Windows credential read-gate (winsec.HasBroadAccess) sees the same
// permissiveness the Unix mode expresses: a mode that permits group/other read
// (or owner execute) grants Everyone read (broad); an owner-only mode restricts
// the DACL to the owner. os.Chmod alone cannot express either on Windows.
func applyUnixModeAsWindowsACL(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if mode.Perm()&0o177 != 0 {
		grantEveryoneReadForTest(t, path)
		return
	}
	if err := winsec.RestrictToOwner(path); err != nil {
		t.Fatalf("RestrictToOwner(%q): %v", path, err)
	}
}

func grantEveryoneReadForTest(t *testing.T, path string) {
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
