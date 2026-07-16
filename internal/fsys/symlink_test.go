package fsys

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecorateSymlinkErr(t *testing.T) {
	privErr := fmt.Errorf("symlink a b: %w", errWinPrivilegeNotHeld)
	tests := []struct {
		name     string
		goos     string
		err      error
		wantHint bool
	}{
		{"nil error passes through", "windows", nil, false},
		{"unix errors undecorated", "linux", privErr, false},
		{"windows privilege error gains hint", "windows", privErr, true},
		{"windows other errors undecorated", "windows", os.ErrNotExist, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decorateSymlinkErr(tt.goos, tt.err)
			if tt.err == nil {
				if got != nil {
					t.Fatalf("decorateSymlinkErr(nil) = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tt.err) && !errors.Is(got, errWinPrivilegeNotHeld) {
				t.Fatalf("decorated error must wrap the original, got %v", got)
			}
			gotHint := strings.Contains(got.Error(), "Developer Mode")
			if gotHint != tt.wantHint {
				t.Fatalf("hint present = %v, want %v (err: %v)", gotHint, tt.wantHint, got)
			}
		})
	}
}

func TestSymlinkCreatesLink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.txt")
	if err := Symlink(target, link); err != nil {
		t.Skipf("symlinks not available in this environment: %v", err)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if got != target {
		t.Fatalf("Readlink = %q, want %q", got, target)
	}
}
