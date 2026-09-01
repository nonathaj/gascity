//go:build !windows

package runtime

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// This test lives in a !windows file because umask is a POSIX process
// attribute: Windows has no syscall.Umask, and no equivalent that can narrow a
// file's permissions at creation time. The behaviour it pins — WritePrivateFile
// chmod'ing back to privateFileMode after creation — is asserted on Windows by
// the winsec ACL tests instead.

// os.CreateTemp asks for 0600, but umask can only narrow what the kernel then
// grants, so under an unusual umask the sidecar would land at something like
// 0400 — no longer the mode the rest of this package asserts, and not writable
// by the owner that has to replace it. The explicit chmod normalises it.
//
// Umask is process-global. This test is not parallel and restores the previous
// value immediately, which is why it does no work between the two calls.
func TestWritePrivateFileNormalisesModeUnderARestrictiveUmask(t *testing.T) {
	// The directory comes first: t.TempDir under a restrictive umask would
	// create a directory the test framework can no longer clean up.
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	bare := filepath.Join(dir, "bare")

	// 0200 clears owner-write, which is the one bit of 0600 a umask can take
	// away; the more familiar 0077 and 0177 leave 0600 untouched.
	previous := syscall.Umask(0o200)
	err := WritePrivateFile(path, []byte("tok-4"))
	// Control: an ordinary 0600 write under the same umask, to show the umask
	// really was in force and the assertion below is about the chmod.
	bareErr := os.WriteFile(bare, []byte("x"), 0o600)
	syscall.Umask(previous)

	if err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}
	if bareErr != nil {
		t.Fatalf("control write: %v", bareErr)
	}
	bareInfo, err := os.Lstat(bare)
	if err != nil {
		t.Fatalf("Lstat control: %v", err)
	}
	if bareInfo.Mode().Perm() == 0o600 {
		t.Skip("umask was not applied to a plain write here; the assertion would prove nothing")
	}

	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want 0600 — the umask was left to decide the sidecar's mode", got)
	}
}
