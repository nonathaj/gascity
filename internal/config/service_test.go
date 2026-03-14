package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

const builtinHealthzContract = "gc.healthz.v1"

func TestParseServiceConfig(t *testing.T) {
	cfg, err := Parse([]byte(`
[workspace]
name = "test-city"

[api]
bind = "127.0.0.1"
port = 9443

[[service]]
name = "review-intake"
publish_mode = "direct"

[service.workflow]
contract = "` + builtinHealthzContract + `"
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Services) != 1 {
		t.Fatalf("len(Services) = %d, want 1", len(cfg.Services))
	}
	svc := cfg.Services[0]
	if svc.Name != "review-intake" {
		t.Fatalf("service name = %q, want review-intake", svc.Name)
	}
	if svc.Workflow.Contract != builtinHealthzContract {
		t.Errorf("workflow.contract = %q, want %q", svc.Workflow.Contract, builtinHealthzContract)
	}
	if svc.PublishModeOrDefault() != "direct" {
		t.Errorf("PublishModeOrDefault() = %q, want direct", svc.PublishModeOrDefault())
	}
	if svc.MountPathOrDefault() != "/svc/review-intake" {
		t.Errorf("MountPathOrDefault() = %q, want /svc/review-intake", svc.MountPathOrDefault())
	}
	if err := ValidateServices(cfg.Services); err != nil {
		t.Fatalf("ValidateServices: %v", err)
	}
}

func TestValidateServicesWorkflowRequiresContract(t *testing.T) {
	err := ValidateServices([]Service{{Name: "review-intake"}})
	if err == nil {
		t.Fatal("expected error for missing workflow.contract")
	}
	if !strings.Contains(err.Error(), "workflow.contract is required") {
		t.Fatalf("error = %v, want missing workflow.contract", err)
	}
}

func TestValidateServicesRejectsUnsupportedKind(t *testing.T) {
	err := ValidateServices([]Service{{
		Name: "review-intake",
		Kind: "mystery",
	}})
	if err == nil {
		t.Fatal("expected error for unsupported service kind")
	}
	if !strings.Contains(err.Error(), `kind must be "workflow" or "proxy_process"`) {
		t.Fatalf("error = %v, want unsupported workflow-only error", err)
	}
}

func TestValidateServicesProxyProcessRequiresCommand(t *testing.T) {
	err := ValidateServices([]Service{{
		Name: "bridge",
		Kind: "proxy_process",
	}})
	if err == nil {
		t.Fatal("expected error for missing process.command")
	}
	if !strings.Contains(err.Error(), "process.command is required") {
		t.Fatalf("error = %v, want missing process.command", err)
	}
}

func TestValidateServicesProxyProcessAcceptsCommand(t *testing.T) {
	err := ValidateServices([]Service{{
		Name: "bridge",
		Kind: "proxy_process",
		Process: ServiceProcessConfig{
			Command: []string{"./scripts/start-bridge.sh"},
		},
	}})
	if err != nil {
		t.Fatalf("ValidateServices: %v", err)
	}
}

func TestExpandCityPacks_ServiceFromPack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "packs/review/pack.toml", `
[pack]
name = "review"
schema = 1

[[service]]
name = "review-intake"

[service.workflow]
contract = "`+builtinHealthzContract+`"
`)

	cfg := &City{
		Workspace: Workspace{Includes: []string{"packs/review"}},
	}
	if _, _, err := ExpandCityPacks(cfg, fsys.OSFS{}, dir); err != nil {
		t.Fatalf("ExpandCityPacks: %v", err)
	}
	if len(cfg.Services) != 1 {
		t.Fatalf("len(Services) = %d, want 1", len(cfg.Services))
	}
	if cfg.Services[0].SourceDir != filepath.Join(dir, "packs/review") {
		t.Errorf("service SourceDir = %q, want %q", cfg.Services[0].SourceDir, filepath.Join(dir, "packs/review"))
	}
	if cfg.Services[0].Workflow.Contract != builtinHealthzContract {
		t.Errorf("workflow.contract = %q, want %q", cfg.Services[0].Workflow.Contract, builtinHealthzContract)
	}
}

func TestExpandPacks_RejectsRigPackServices(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "packs/review/pack.toml", `
[pack]
name = "review"
schema = 1

[[service]]
name = "review-intake"

[service.workflow]
contract = "`+builtinHealthzContract+`"
`)

	cfg := &City{
		Rigs: []Rig{{
			Name:     "product",
			Path:     "/tmp/product",
			Includes: []string{"packs/review"},
		}},
	}
	err := ExpandPacks(cfg, fsys.OSFS{}, dir, nil)
	if err == nil {
		t.Fatal("expected rig pack service rejection")
	}
	if !strings.Contains(err.Error(), "[[service]] is only allowed in city-scoped packs") {
		t.Fatalf("error = %v, want rig pack service rejection", err)
	}
}
