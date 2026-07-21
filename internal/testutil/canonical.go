package testutil

import "path/filepath"

// tempDirer matches *testing.T without importing the testing package
// into this non-test file (same pattern as the watchdog's runner).
type tempDirer interface {
	Helper()
	TempDir() string
}

// CanonicalTempDir returns t.TempDir() with symlinks and Windows 8.3
// short names resolved. CI runners hand out short-form temp roots
// (C:\Users\RUNNER~1\...) while production path canonicalization
// expands to the long form — expectations built from a raw TempDir
// then mismatch. Locally invisible whenever the username is short
// enough to need no 8.3 alias, so always use this when a test compares
// canonicalized production output against fixture paths.
func CanonicalTempDir(t tempDirer) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}
