//go:build !windows

package sessionlog

import (
	"os"
	"testing"
)

// makeFileUnopenable makes any subsequent os.Open of path fail for the
// test's duration. On Unix, chmod 0 removes owner read permission.
func makeFileUnopenable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod 0 %q: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}
