//go:build windows

package main

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// dupFileWithCosmeticName returns a second handle for f whose Name()
// deliberately does not match the underlying path, modeling fd 1/2 after a
// parent process redirected them to a log file.
func dupFileWithCosmeticName(t *testing.T, f *os.File, name string) *os.File {
	t.Helper()
	cur := windows.CurrentProcess()
	var dup windows.Handle
	if err := windows.DuplicateHandle(cur, windows.Handle(f.Fd()), cur, &dup, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		t.Fatalf("DuplicateHandle: %v", err)
	}
	dupFile := os.NewFile(uintptr(dup), name)
	if dupFile == nil {
		t.Fatal("os.NewFile returned nil for duplicated handle")
	}
	return dupFile
}
