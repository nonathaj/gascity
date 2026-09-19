// deviceOf reads st_dev straight from the kernel, which is a Unix concept with
// no Windows equivalent (os.FileInfo.Sys() is *syscall.Stat_t only there). The
// tests that call it use it to prove their own ephemeral-filesystem
// precondition, so they are Unix-only too and live beside it.
//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
)

// deviceOf returns the filesystem device path lives on, read straight from the
// kernel. It is deliberately not routed through internal/pathdurability: these
// tests use it to establish their own precondition, and a precondition proved
// with the code under test cannot detect that code breaking.
func deviceOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat %q: no syscall.Stat_t available", path)
	}
	// st_dev is int32 on darwin and uint64 on linux; widen explicitly.
	return uint64(st.Dev)
}
