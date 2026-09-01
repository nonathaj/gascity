//go:build windows

package runtime

import (
	"fmt"
	"os"
)

// PrivateDirACL narrows path's Windows ACL so only its owner (plus LocalSystem
// and Administrators) can reach it. It is a hook rather than a direct call
// because this package is pinned to standard-library imports by
// RUNTIME-INV-001 — it is the in-process expression of the Runtime Provider
// Protocol contract, and the ACL helper (internal/winsec) is SDK code. The gc
// binary installs it during init; see cmd/gc/private_dir_acl_windows.go.
//
// Leaving it nil is a real gap, not a formality, so it is deliberately visible:
// a build that never installs it gets the Unix-shaped checks below and no ACL
// narrowing at all.
var PrivateDirACL func(path string) error

// narrowPrivateDir enforces the owner-only contract with an ACL rather than
// mode bits.
//
// Neither half of the Unix check has a meaning here. There is no uid to compare
// — ownership is a SID — and os.Chmod cannot revoke access on Windows at all:
// it only toggles the read-only bit, so a 0700 chmod leaves a
// credential-adjacent directory readable by everyone. syscall.Stat_t does not
// exist on this platform either, which is what made the shared implementation
// fail to build rather than merely fail to protect.
func narrowPrivateDir(path string, _ os.FileInfo, _ int) error {
	if PrivateDirACL == nil {
		return nil
	}
	if err := PrivateDirACL(path); err != nil {
		return fmt.Errorf("tightening private directory %q: %w", path, err)
	}
	return nil
}
