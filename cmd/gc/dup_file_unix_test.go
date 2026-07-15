//go:build !windows

package main

import (
	"os"
	"syscall"
	"testing"
)

// dupFileWithCosmeticName returns a second descriptor for f whose Name()
// deliberately does not match the underlying path, modeling fd 1/2 after a
// service manager redirected them to a log file.
func dupFileWithCosmeticName(t *testing.T, f *os.File, name string) *os.File {
	t.Helper()
	dupFD, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		t.Fatalf("dup fd: %v", err)
	}
	dup := os.NewFile(uintptr(dupFD), name)
	if dup == nil {
		t.Fatal("os.NewFile returned nil for duplicated fd")
	}
	return dup
}
