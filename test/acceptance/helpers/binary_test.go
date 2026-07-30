package acceptancehelpers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildGCUsesOverrideBinary(t *testing.T) {
	t.Helper()

	tmpDir := t.TempDir()
	override := filepath.Join(tmpDir, "gc-external")
	if err := os.WriteFile(override, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write override binary: %v", err)
	}

	t.Setenv("GC_ACCEPTANCE_GC_BIN", override)

	got := BuildGC(t.TempDir())
	if got != override {
		t.Fatalf("BuildGC() = %q, want %q", got, override)
	}
}

func TestRunGCUsesExactBinaryOverridePath(t *testing.T) {
	t.Helper()

	tmpDir := t.TempDir()
	gcHome := filepath.Join(tmpDir, "home")
	runtimeDir := filepath.Join(tmpDir, "runtime")
	if err := os.MkdirAll(gcHome, 0o755); err != nil {
		t.Fatalf("mkdir gcHome: %v", err)
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		t.Fatalf("mkdir runtimeDir: %v", err)
	}

	override := filepath.Join(tmpDir, "custom-binary-name")
	script := "#!/bin/sh\necho override:$0 $*\n"
	if err := os.WriteFile(override, []byte(script), 0o755); err != nil {
		t.Fatalf("write override binary: %v", err)
	}

	env := NewEnv(override, gcHome, runtimeDir)
	out, err := RunGC(env, "", "version")
	if err != nil {
		t.Fatalf("RunGC: %v\n%s", err, out)
	}
	// $0 is compared in slash form. execshim hands sh a slash-normalized script
	// path on purpose: pack scripts locate their siblings with
	// `case "$0" in */*)`, and a native Windows path has no forward slash, so
	// that glob silently fell through and resolved the script's directory to the
	// caller's working directory. The shell therefore reports a POSIX-shaped $0
	// while filepath.Join here produces a native one; ToSlash puts both in the
	// same space without weakening the assertion. No-op on Unix.
	if !strings.Contains(filepath.ToSlash(out), "override:"+filepath.ToSlash(override)+" version") {
		t.Fatalf("RunGC() output = %q, want override invocation for %q", out, override)
	}
}
