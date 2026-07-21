//go:build !windows

package testutil

import "os"

// MakeFileUnopenable makes any subsequent open/read of path fail for
// the test's duration (doctrine class T4). On Unix, chmod 0 removes
// owner read permission; running as root bypasses permission checks,
// so the test skips there.
func MakeFileUnopenable(t failer, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses file permission checks")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("chmod 0 %q: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}
