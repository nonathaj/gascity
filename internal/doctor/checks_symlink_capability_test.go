package doctor

import (
	"errors"
	"strings"
	"testing"
)

func TestSymlinkCapabilityCheckOK(t *testing.T) {
	c := &SymlinkCapabilityCheck{probeDir: t.TempDir()}
	r := c.Run(&CheckContext{})
	if r.Status != StatusOK {
		t.Skipf("symlinks unavailable in this environment: %v", r.Details)
	}
	if r.Name != "symlink-capability" {
		t.Fatalf("Name = %q", r.Name)
	}
}

func TestSymlinkCapabilityCheckFailureCarriesGuidance(t *testing.T) {
	c := &SymlinkCapabilityCheck{
		probeDir: t.TempDir(),
		symlink: func(_, _ string) error {
			return errors.New("A required privilege is not held by the client.")
		},
	}
	r := c.Run(&CheckContext{})
	if r.Status != StatusError {
		t.Fatalf("Status = %v, want Error", r.Status)
	}
	if r.FixHint == "" {
		t.Fatal("failing check must carry a FixHint")
	}
	if len(r.Details) == 0 || !strings.Contains(r.Details[0], "privilege") {
		t.Fatalf("Details should carry the underlying error, got %v", r.Details)
	}
}

func TestSymlinkCapabilityCheckProbeCleansUp(t *testing.T) {
	dir := t.TempDir()
	c := &SymlinkCapabilityCheck{probeDir: dir}
	r := c.Run(&CheckContext{})
	if r.Status != StatusOK {
		t.Skipf("symlinks unavailable in this environment: %v", r.Details)
	}
	// Second run must not trip over the first run's probe artifact.
	if r2 := c.Run(&CheckContext{}); r2.Status != StatusOK {
		t.Fatalf("second Run status = %v, want OK (stale probe not cleaned?)", r2.Status)
	}
}
