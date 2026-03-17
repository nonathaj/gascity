package api

import (
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestResolveAgentWorkDirForNameUsesWorkDirTemplate(t *testing.T) {
	cityPath := t.TempDir()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "gastown"},
		Rigs:      []config.Rig{{Name: "demo", Path: filepath.Join(cityPath, "repos", "demo")}},
	}
	agent := config.Agent{
		Name:    "refinery",
		Dir:     "demo",
		WorkDir: ".gc/worktrees/{{.Rig}}/{{.AgentBase}}",
	}

	got := resolveAgentWorkDirForName(cityPath, cfg, agent, "demo/refinery")
	want := filepath.Join(cityPath, ".gc", "worktrees", "demo", "refinery")
	if got != want {
		t.Fatalf("resolveAgentWorkDirForName() = %q, want %q", got, want)
	}
}

func TestResolveAgentWorkDirForNameDefaultsRigScopedAgentsToRigRoot(t *testing.T) {
	cityPath := t.TempDir()
	rigRoot := filepath.Join(t.TempDir(), "demo-repo")
	cfg := &config.City{
		Workspace: config.Workspace{Name: "gastown"},
		Rigs:      []config.Rig{{Name: "demo", Path: rigRoot}},
	}
	agent := config.Agent{
		Name: "refinery",
		Dir:  "demo",
	}

	got := resolveAgentWorkDirForName(cityPath, cfg, agent, "demo/refinery")
	if got != rigRoot {
		t.Fatalf("resolveAgentWorkDirForName() = %q, want %q", got, rigRoot)
	}
}

func TestResolveAgentWorkDirForNameUsesPoolInstanceBase(t *testing.T) {
	cityPath := t.TempDir()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "gastown"},
		Rigs:      []config.Rig{{Name: "demo", Path: filepath.Join(cityPath, "repos", "demo")}},
	}
	agent := config.Agent{
		Name:    "polecat",
		Dir:     "demo",
		WorkDir: ".gc/worktrees/{{.Rig}}/polecats/{{.AgentBase}}",
		Pool:    &config.PoolConfig{Min: 0, Max: 3},
	}

	got := resolveAgentWorkDirForName(cityPath, cfg, agent, "demo/polecat-2")
	want := filepath.Join(cityPath, ".gc", "worktrees", "demo", "polecats", "polecat-2")
	if got != want {
		t.Fatalf("resolveAgentWorkDirForName() = %q, want %q", got, want)
	}
}

func TestCanAttributeSessionUsesResolvedWorkDir(t *testing.T) {
	cityPath := t.TempDir()
	cfg := &config.City{
		Workspace: config.Workspace{Name: "gastown", Provider: "claude"},
		Rigs:      []config.Rig{{Name: "demo", Path: filepath.Join(cityPath, "repos", "demo")}},
		Agents: []config.Agent{
			{Name: "refinery", Dir: "demo", WorkDir: ".gc/worktrees/{{.Rig}}/refinery"},
			{Name: "witness", Dir: "demo", WorkDir: ".gc/agents/{{.Rig}}/witness"},
		},
	}

	if !canAttributeSession(cfg.Agents[0], "demo/refinery", cfg, cityPath) {
		t.Fatal("canAttributeSession() = false, want true for distinct workdirs")
	}
}

func TestResolveSessionTemplateUsesConfiguredWorkDir(t *testing.T) {
	state := newFakeState(t)
	state.cfg.Agents[0].WorkDir = ".gc/worktrees/{{.Rig}}/{{.AgentBase}}"
	srv := New(state)

	_, workDir, _, _, err := srv.resolveSessionTemplate("myrig/worker")
	if err != nil {
		t.Fatalf("resolveSessionTemplate: %v", err)
	}

	want := filepath.Join(state.cityPath, ".gc", "worktrees", "myrig", "worker")
	if workDir != want {
		t.Fatalf("resolveSessionTemplate() workDir = %q, want %q", workDir, want)
	}
}
