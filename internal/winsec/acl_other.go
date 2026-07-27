//go:build !windows

// Package winsec restricts filesystem access to the owner. On Windows that means a
// protected owner-only DACL, because os.Chmod there only toggles the read-only bit and
// cannot revoke access; everywhere else the caller's chmod is the native mechanism, so
// the entry points in this file are no-ops that exist to keep call sites platform-free.
package winsec

// RestrictToOwner is a no-op on non-Windows platforms: callers restrict access
// with os.Chmod (0700/0600), which is the native mechanism there. It exists so
// cross-platform code can call a single restriction helper unconditionally.
func RestrictToOwner(string) error { return nil }

// IsRestrictedToOwner reports true on non-Windows platforms: access restriction
// is expressed and verified through Unix mode bits there, so this ACL-oriented
// check is not the relevant assertion and never fails a caller.
func IsRestrictedToOwner(string) (bool, error) { return true, nil }

// HasBroadAccess reports false on non-Windows platforms: the Unix mode gate is
// the relevant permissiveness check there, not the ACL. Callers apply the mode
// check directly on Unix and only consult this on Windows.
func HasBroadAccess(string) (bool, error) { return false, nil }
