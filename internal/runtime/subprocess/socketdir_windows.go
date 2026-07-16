//go:build windows

package subprocess

import "os"

// validatePrivateSocketOwnership is the Windows counterpart of the Unix socket
// directory ownership check. Windows has no Unix mode bits, setuid/setgid/
// sticky bits, or numeric uid, so those checks do not apply — the per-user
// temp path is access-controlled by NTFS ACLs. The directory-ness is already
// validated by the caller, so there is nothing further to enforce here.
func validatePrivateSocketOwnership(_ string, _ os.FileInfo, _ int) error {
	return nil
}
