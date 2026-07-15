//go:build !windows

package convergence

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// FIFO creation is a Unix-only primitive (syscall.Mkfifo has no Windows
// definition), so this half of the ValidateArtifactDir suite is build-split.
func TestValidateArtifactDir_FIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")

	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skipf("mkfifo not available: %v", err)
	}

	err := ValidateArtifactDir(dir)
	if err == nil {
		t.Fatal("expected error for FIFO in artifact directory")
	}
	if !strings.Contains(err.Error(), "unsafe file type") {
		t.Errorf("error should mention unsafe file type, got: %v", err)
	}
}
