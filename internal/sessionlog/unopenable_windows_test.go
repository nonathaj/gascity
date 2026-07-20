//go:build windows

package sessionlog

import (
	"testing"

	"golang.org/x/sys/windows"
)

// makeFileUnopenable makes any subsequent os.Open of path fail for the
// test's duration. Mode bits cannot deny the owner on NTFS, so the
// Windows equivalent of chmod 0 is holding an exclusive no-share
// handle: every later open fails with ERROR_SHARING_VIOLATION.
func makeFileUnopenable(t *testing.T, path string) {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatalf("UTF16PtrFromString(%q): %v", path, err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("CreateFile(%q, no-share): %v", path, err)
	}
	t.Cleanup(func() { _ = windows.CloseHandle(h) })
}
