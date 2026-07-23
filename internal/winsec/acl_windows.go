//go:build windows

// Package winsec provides the Windows-native equivalent of restrictive Unix
// mode bits (chmod 0700/0600): a protected DACL that grants access only to the
// file owner, LocalSystem, and Administrators. On Windows os.Chmod cannot revoke
// access — it only toggles the read-only bit — so sensitive paths (.gc/secrets,
// managed-Dolt state, registry credentials) are unprotected without an explicit
// ACL. On non-Windows platforms the exported functions are no-ops; callers keep
// using os.Chmod there.
package winsec

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// RestrictToOwner sets a protected DACL on path granting full control to the
// current user, LocalSystem, and the Administrators group, and to no one else —
// the Windows analogue of chmod 0700/0600. The DACL is marked protected so
// inherited ACEs from a parent directory cannot re-widen it, and directory ACEs
// carry container/object inheritance so files created underneath inherit the
// same restriction. path may name a file or a directory.
func RestrictToOwner(path string) error {
	ownerSID, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("resolving current user SID: %w", err)
	}
	systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("creating LocalSystem SID: %w", err)
	}
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("creating Administrators SID: %w", err)
	}

	dacl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{
		fullControl(ownerSID),
		fullControl(systemSID),
		fullControl(adminSID),
	}, nil)
	if err != nil {
		return fmt.Errorf("building DACL: %w", err)
	}

	// PROTECTED_DACL_SECURITY_INFORMATION drops any inherited ACEs so the
	// restrictive DACL is authoritative regardless of what the parent grants.
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, // leave owner unchanged
		nil, // leave primary group unchanged
		dacl,
		nil, // leave SACL unchanged
	); err != nil {
		return fmt.Errorf("setting DACL on %q: %w", path, err)
	}
	return nil
}

// IsRestrictedToOwner reports whether path carries a protected DACL that does
// not grant access to the broad principals Unix mode bits exclude — Everyone,
// Authenticated Users, and the Users group. It is the verification counterpart
// to RestrictToOwner, used by tests to assert the restriction the way Unix tests
// assert 0700/0600 mode bits.
func IsRestrictedToOwner(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return false, fmt.Errorf("reading security info for %q: %w", path, err)
	}
	control, _, err := sd.Control()
	if err != nil {
		return false, fmt.Errorf("reading control flags for %q: %w", path, err)
	}
	// A non-protected DACL can inherit broad grants from a parent; require the
	// same authoritative restriction RestrictToOwner applies.
	if control&windows.SE_DACL_PROTECTED == 0 {
		return false, nil
	}
	// SDDL is the stable serialization of the descriptor; a restricted DACL must
	// not name any broad principal, in either short-form or full-SID spelling.
	sddl := sd.String()
	dacl := sddl
	if idx := strings.Index(sddl, "D:"); idx >= 0 {
		dacl = sddl[idx:]
		if s := strings.Index(dacl, "S:"); s >= 0 { // trim a trailing SACL clause
			dacl = dacl[:s]
		}
	}
	for _, broad := range broadPrincipals {
		if strings.Contains(dacl, broad) {
			return false, nil
		}
	}
	return true, nil
}

// broadPrincipals are the SDDL spellings (short alias and canonical SID) of the
// principals restrictive Unix modes exclude: Everyone (WD / S-1-1-0),
// Authenticated Users (AU / S-1-5-11), and the built-in Users group
// (BU / S-1-5-32-545). An access ACE naming any of them means the path is not
// owner-restricted.
var broadPrincipals = []string{
	";WD)", ";S-1-1-0)",
	";AU)", ";S-1-5-11)",
	";BU)", ";S-1-5-32-545)",
}

func fullControl(sid *windows.SID) windows.EXPLICIT_ACCESS {
	return windows.EXPLICIT_ACCESS{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_UNKNOWN,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}
}

// currentUserSID returns a heap-owned copy of the running process's user SID.
// The token's own SID buffer is freed when the *Tokenuser is collected, so the
// copy keeps the value valid for the lifetime of the DACL we build from it.
func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return user.User.Sid.Copy()
}
