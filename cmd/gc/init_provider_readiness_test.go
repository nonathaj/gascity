package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
)

func TestMaybePrintWizardProviderGuidanceNeedsAuth(t *testing.T) {
	oldProbe := initProbeProvidersReadiness
	initProbeProvidersReadiness = func(_ context.Context, _ []string, fresh bool) (map[string]api.ReadinessItem, error) {
		if fresh {
			t.Fatal("wizard guidance should use cached probe mode")
		}
		return map[string]api.ReadinessItem{
			"claude": {
				Name:        "claude",
				Kind:        api.ProbeKindProvider,
				DisplayName: "Claude Code",
				Status:      api.ProbeStatusNeedsAuth,
			},
		}, nil
	}
	t.Cleanup(func() { initProbeProvidersReadiness = oldProbe })

	var stdout bytes.Buffer
	maybePrintWizardProviderGuidance(wizardConfig{
		interactive: true,
		provider:    "claude",
	}, &stdout)

	out := stdout.String()
	if !strings.Contains(out, "Claude Code is not signed in yet") {
		t.Fatalf("stdout = %q, want readiness note", out)
	}
}

func TestFinalizeInitBlocksProviderReadinessBeforeSupervisorRegistration(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	t.Setenv("GC_DOLT", "skip")
	configureIsolatedRuntimeEnv(t)

	cityPath := filepath.Join(t.TempDir(), "bright-lights")
	var initStdout, initStderr bytes.Buffer
	code := doInit(fsys.OSFS{}, cityPath, wizardConfig{
		configName: "tutorial",
		provider:   "claude",
	}, &initStdout, &initStderr)
	if code != 0 {
		t.Fatalf("doInit = %d, want 0: %s", code, initStderr.String())
	}

	oldProbe := initProbeProvidersReadiness
	initProbeProvidersReadiness = func(_ context.Context, _ []string, fresh bool) (map[string]api.ReadinessItem, error) {
		if !fresh {
			t.Fatal("finalizeInit should force a fresh readiness probe")
		}
		return map[string]api.ReadinessItem{
			"claude": {
				Name:        "claude",
				Kind:        api.ProbeKindProvider,
				DisplayName: "Claude Code",
				Status:      api.ProbeStatusNeedsAuth,
			},
		}, nil
	}
	t.Cleanup(func() { initProbeProvidersReadiness = oldProbe })

	calledRegister := false
	oldRegister := registerCityWithSupervisorTestHook
	registerCityWithSupervisorTestHook = func(_ string, _ string, _ io.Writer, _ io.Writer) (bool, int) {
		calledRegister = true
		return true, 0
	}
	t.Cleanup(func() { registerCityWithSupervisorTestHook = oldRegister })

	var stdout, stderr bytes.Buffer
	code = finalizeInit(cityPath, &stdout, &stderr, initFinalizeOptions{
		commandName: "gc init",
	})
	if code != 1 {
		t.Fatalf("finalizeInit = %d, want 1", code)
	}
	if calledRegister {
		t.Fatal("registerCityWithSupervisor should not be called when provider readiness blocks init")
	}
	if !strings.Contains(stderr.String(), "startup is blocked by provider readiness") {
		t.Fatalf("stderr = %q, want provider readiness block message", stderr.String())
	}
	if !strings.Contains(stderr.String(), "run `claude auth login`") {
		t.Fatalf("stderr = %q, want Claude fix hint", stderr.String())
	}
}

func TestFinalizeInitWarnsForUnprobeableCustomProviderAndContinues(t *testing.T) {
	t.Setenv("GC_BEADS", "file")
	t.Setenv("GC_DOLT", "skip")
	configureIsolatedRuntimeEnv(t)

	cityPath := filepath.Join(t.TempDir(), "bright-lights")
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureCityScaffold(cityPath); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultCity("bright-lights")
	cfg.Workspace.Provider = "wrapper"
	cfg.Providers = map[string]config.ProviderSpec{
		"wrapper": {
			DisplayName: "Wrapper Agent",
			Command:     "sh",
		},
	}
	content, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	oldProbe := initProbeProvidersReadiness
	initProbeProvidersReadiness = func(_ context.Context, providers []string, _ bool) (map[string]api.ReadinessItem, error) {
		t.Fatalf("unexpected readiness probe for unprobeable provider: %v", providers)
		return nil, nil
	}
	t.Cleanup(func() { initProbeProvidersReadiness = oldProbe })

	oldRegister := registerCityWithSupervisorTestHook
	registerCityWithSupervisorTestHook = func(_ string, _ string, _ io.Writer, _ io.Writer) (bool, int) {
		return true, 0
	}
	t.Cleanup(func() { registerCityWithSupervisorTestHook = oldRegister })

	var stdout, stderr bytes.Buffer
	code := finalizeInit(cityPath, &stdout, &stderr, initFinalizeOptions{
		commandName: "gc init",
	})
	if code != 0 {
		t.Fatalf("finalizeInit = %d, want 0: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Wrapper Agent is referenced, but Gas City cannot verify its login state automatically yet.") {
		t.Fatalf("stdout = %q, want unprobeable-provider warning", stdout.String())
	}
}
