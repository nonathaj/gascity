package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

func TestResolveSessionTemplateReturnsConfiguredTemplate(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "researcher"},
			{Name: "worker", Dir: "rig-a"},
		},
	}

	a, ok := resolveSessionTemplate(cfg, "worker", "rig-a")
	if !ok {
		t.Fatal("expected to resolve rig-scoped template from current rig context")
	}
	if got := a.QualifiedName(); got != "rig-a/worker" {
		t.Fatalf("QualifiedName = %q, want rig-a/worker", got)
	}
}

func TestResolveSessionTemplateRejectsDerivedPoolMember(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "worker", Pool: &config.PoolConfig{Min: 0, Max: 5}},
		},
	}

	if _, ok := resolveSessionTemplate(cfg, "worker-2", ""); ok {
		t.Fatal("expected pooled instance name to be rejected as a session template")
	}
}

func TestFindAgentByQualifiedRejectsLegacyTemplateInstanceSyntax(t *testing.T) {
	cfg := &config.City{
		Agents: []config.Agent{
			{Name: "researcher"},
		},
	}

	if _, ok := findAgentByQualified(cfg, "researcher/spike-1"); ok {
		t.Fatal("expected legacy template/instance syntax to stop resolving")
	}
}
