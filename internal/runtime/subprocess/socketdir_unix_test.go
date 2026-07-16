//go:build !windows

package subprocess

import (
	"os"
	"syscall"
	"testing"
)

func requirePrivateSocketOwnership(t *testing.T, path string, info os.FileInfo) {
	t.Helper()
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("%q permissions = %04o, want 0700", path, got)
	}
	if special := info.Mode() & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky); special != 0 {
		t.Fatalf("%q special mode bits = %v, want none", path, special)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("%q ownership metadata = %T, want *syscall.Stat_t", path, info.Sys())
	}
	if got, want := stat.Uid, uint32(os.Geteuid()); got != want {
		t.Fatalf("%q uid = %d, want %d", path, got, want)
	}
}
