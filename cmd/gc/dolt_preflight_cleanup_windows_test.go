//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPlatformFileOpenStateWindows validates the native open-state probe:
// a file held open by another handle is detected (exclusive open → sharing
// violation), while a closed or missing file is not.
func TestPlatformFileOpenStateWindows(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "gone.db")
	if open, checked, err := platformFileOpenState(missing); err != nil || !checked || open {
		t.Fatalf("missing file: open=%v checked=%v err=%v; want open=false checked=true", open, checked, err)
	}

	closed := filepath.Join(dir, "closed.db")
	if err := os.WriteFile(closed, []byte("x"), 0o600); err != nil {
		t.Fatalf("write closed file: %v", err)
	}
	if open, checked, err := platformFileOpenState(closed); err != nil || !checked || open {
		t.Fatalf("closed file: open=%v checked=%v err=%v; want open=false checked=true", open, checked, err)
	}

	held := filepath.Join(dir, "held.db")
	if err := os.WriteFile(held, []byte("x"), 0o600); err != nil {
		t.Fatalf("write held file: %v", err)
	}
	f, err := os.Open(held) // holds the file open with the default (shared) mode
	if err != nil {
		t.Fatalf("open held file: %v", err)
	}
	defer f.Close()
	if open, checked, err := platformFileOpenState(held); err != nil || !checked || !open {
		t.Fatalf("held file: open=%v checked=%v err=%v; want open=true checked=true", open, checked, err)
	}
}
