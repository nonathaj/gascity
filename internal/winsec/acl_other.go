//go:build !windows

package winsec

// RestrictToOwner is a no-op on non-Windows platforms: callers restrict access
// with os.Chmod (0700/0600), which is the native mechanism there. It exists so
// cross-platform code can call a single restriction helper unconditionally.
func RestrictToOwner(string) error { return nil }

// IsRestrictedToOwner reports true on non-Windows platforms: access restriction
// is expressed and verified through Unix mode bits there, so this ACL-oriented
// check is not the relevant assertion and never fails a caller.
func IsRestrictedToOwner(string) (bool, error) { return true, nil }
