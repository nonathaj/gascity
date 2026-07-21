//go:build !windows

package eventexport

import (
	"os"
	"testing"
)

// makeFileUnreadable makes any subsequent read of path fail for the
// test's duration. On Unix, chmod 0 removes owner read permission.
func makeFileUnreadable(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses file permission checks")
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod 0 %q: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}
