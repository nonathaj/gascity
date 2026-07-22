package fsys

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveDirIfEmptyRemovesEmptyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := RemoveDirIfEmpty(dir); err != nil {
		t.Fatalf("RemoveDirIfEmpty: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir still exists: stat err = %v", err)
	}
}

func TestRemoveDirIfEmptyToleratesNonEmptyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "occupied")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := RemoveDirIfEmpty(dir); err != nil {
		t.Fatalf("RemoveDirIfEmpty tolerated a shared dir with error: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("shared dir should be left in place, stat err = %v", err)
	}
}

func TestRemoveDirIfEmptyMissingDirIsOK(t *testing.T) {
	if err := RemoveDirIfEmpty(filepath.Join(t.TempDir(), "never-existed")); err != nil {
		t.Fatalf("RemoveDirIfEmpty on missing dir: %v", err)
	}
}
