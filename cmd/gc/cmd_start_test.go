package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMergeEnvEmptyMaps(t *testing.T) {
	got := mergeEnv(map[string]string{}, map[string]string{})
	if got != nil {
		t.Errorf("mergeEnv(empty, empty) = %v, want nil", got)
	}
}

func TestMergeEnvNilAndValues(t *testing.T) {
	got := mergeEnv(nil, map[string]string{"A": "1"})
	if got["A"] != "1" {
		t.Errorf("mergeEnv[A] = %q, want %q", got["A"], "1")
	}
}

func TestPassthroughEnvIncludesPath(t *testing.T) {
	// PATH is always set in a normal environment.
	got := passthroughEnv()
	if _, ok := got["PATH"]; !ok {
		t.Error("passthroughEnv() missing PATH")
	}
}

func TestPassthroughEnvPicksUpGCBeads(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	got := passthroughEnv()
	if got["GC_BEADS"] != "file" {
		t.Errorf("passthroughEnv()[GC_BEADS] = %q, want %q", got["GC_BEADS"], "file")
	}
}

func TestPassthroughEnvOmitsUnset(t *testing.T) {
	t.Setenv("GC_DOLT", "")
	got := passthroughEnv()
	if _, ok := got["GC_DOLT"]; ok {
		t.Error("passthroughEnv() should omit empty GC_DOLT")
	}
}

func TestMergeEnvOverrideOrder(t *testing.T) {
	a := map[string]string{"KEY": "first", "A": "a"}
	b := map[string]string{"KEY": "second", "B": "b"}
	got := mergeEnv(a, b)
	if got["KEY"] != "second" {
		t.Errorf("mergeEnv override: KEY = %q, want %q", got["KEY"], "second")
	}
	if got["A"] != "a" {
		t.Errorf("mergeEnv: A = %q, want %q", got["A"], "a")
	}
	if got["B"] != "b" {
		t.Errorf("mergeEnv: B = %q, want %q", got["B"], "b")
	}
}

func TestMergeEnvAllNil(t *testing.T) {
	got := mergeEnv(nil, nil, nil)
	if got != nil {
		t.Errorf("mergeEnv(nil, nil, nil) = %v, want nil", got)
	}
}

func TestPassthroughEnvDoltConnectionVars(t *testing.T) {
	t.Setenv("GC_DOLT_HOST", "dolt.gc.svc.cluster.local")
	t.Setenv("GC_DOLT_PORT", "3307")
	t.Setenv("GC_DOLT_USER", "agent")
	t.Setenv("GC_DOLT_PASSWORD", "s3cret")

	got := passthroughEnv()

	for _, key := range []string{"GC_DOLT_HOST", "GC_DOLT_PORT", "GC_DOLT_USER", "GC_DOLT_PASSWORD"} {
		if _, ok := got[key]; !ok {
			t.Errorf("passthroughEnv() missing %s", key)
		}
	}
	if got["GC_DOLT_HOST"] != "dolt.gc.svc.cluster.local" {
		t.Errorf("GC_DOLT_HOST = %q, want %q", got["GC_DOLT_HOST"], "dolt.gc.svc.cluster.local")
	}
	if got["GC_DOLT_PORT"] != "3307" {
		t.Errorf("GC_DOLT_PORT = %q, want %q", got["GC_DOLT_PORT"], "3307")
	}
}

func TestPassthroughEnvOmitsUnsetDoltVars(t *testing.T) {
	// Ensure the vars are NOT set.
	for _, key := range []string{"GC_DOLT_HOST", "GC_DOLT_PORT", "GC_DOLT_USER", "GC_DOLT_PASSWORD"} {
		t.Setenv(key, "")
	}

	got := passthroughEnv()

	for _, key := range []string{"GC_DOLT_HOST", "GC_DOLT_PORT", "GC_DOLT_USER", "GC_DOLT_PASSWORD"} {
		if _, ok := got[key]; ok {
			t.Errorf("passthroughEnv() should omit empty %s", key)
		}
	}
}

func TestPassthroughEnvStripsClaudeNesting(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")

	got := passthroughEnv()

	// Should be present but empty so tmux -e overrides the inherited server env.
	if v, ok := got["CLAUDECODE"]; !ok || v != "" {
		t.Errorf("CLAUDECODE = %q (present=%v), want empty string present", v, ok)
	}
	if v, ok := got["CLAUDE_CODE_ENTRYPOINT"]; !ok || v != "" {
		t.Errorf("CLAUDE_CODE_ENTRYPOINT = %q (present=%v), want empty string present", v, ok)
	}
}

func TestPassthroughEnvSkipsClaudeNestingWhenUnset(t *testing.T) {
	t.Setenv("CLAUDECODE", "")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "")

	got := passthroughEnv()

	// When not set in parent, don't inject them at all.
	if _, ok := got["CLAUDECODE"]; ok {
		t.Error("CLAUDECODE should not be present when unset in parent")
	}
	if _, ok := got["CLAUDE_CODE_ENTRYPOINT"]; ok {
		t.Error("CLAUDE_CODE_ENTRYPOINT should not be present when unset in parent")
	}
}

func TestStageHookFilesIncludesCodexAndCopilotExecutableHooks(t *testing.T) {
	cityDir := filepath.Join(t.TempDir(), "city")
	workDir := filepath.Join(cityDir, "worker")
	for _, rel := range []string{
		filepath.Join(".codex", "hooks.json"),
		filepath.Join(".github", "hooks", "gascity.json"),
		filepath.Join(".github", "copilot-instructions.md"),
	} {
		path := filepath.Join(workDir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	got := stageHookFiles(nil, cityDir, workDir)
	rels := make(map[string]bool, len(got))
	for _, entry := range got {
		rels[entry.RelDst] = true
	}
	for _, rel := range []string{
		filepath.Join(".codex", "hooks.json"),
		filepath.Join(".github", "hooks", "gascity.json"),
		filepath.Join(".github", "copilot-instructions.md"),
	} {
		if !rels[rel] {
			t.Errorf("stageHookFiles() missing %q", rel)
		}
	}
}
