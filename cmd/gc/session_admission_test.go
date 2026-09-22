package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

// Regression: pool admission must not leave named sessions outside the city limit.
func TestWorkspaceSessionAdmissionIncludesNamed(t *testing.T) {
	for _, cap := range []int{1, 32} {
		t.Run(fmt.Sprintf("cap=%d", cap), func(t *testing.T) {
			env := newReconcilerTestEnv()
			env.clk.Time = time.Now().UTC()
			env.cfg.Workspace = config.Workspace{Name: "efficiency-probe", MaxActiveSessions: intPtr(cap)}
			env.cfg.Daemon.MaxWakesPerTick = intPtr(64)
			env.cfg.Providers = map[string]config.ProviderSpec{"probe": {Command: "true"}}
			env.cfg.Agents = []config.Agent{{Name: "worker", Provider: "probe", MinActiveSessions: intPtr(0), MaxActiveSessions: intPtr(1)}}
			// One pool request and cap named sessions: every session uses the same
			// provider, so exemption/classification cannot explain the overshoot.
			for i := 0; i < cap; i++ {
				name := fmt.Sprintf("resident-%d", i)
				env.cfg.Agents = append(env.cfg.Agents, config.Agent{Name: name, Provider: "probe", MaxActiveSessions: intPtr(1)})
				env.cfg.NamedSessions = append(env.cfg.NamedSessions, config.NamedSession{Template: name, Mode: "always"})
			}
			if _, err := env.store.Create(beads.Bead{Type: "task", Status: "open", Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "worker"}}); err != nil {
				t.Fatal(err)
			}
			cityPath := t.TempDir()
			result := buildDesiredState("efficiency-probe", cityPath, env.clk.Now(), env.cfg, env.sp, env.store, &env.stderr)
			if result.SessionQueryPartial {
				t.Fatalf("invalid fixture: partial session read: %s", env.stderr.String())
			}
			env.desiredState = result.State
			configured := configuredSessionNames(env.cfg, "efficiency-probe", env.store)
			syncSessionBeads(cityPath, env.store, result.State, env.sp, configured, env.cfg, env.clk, &env.stderr, true)
			sessions, err := loadSessionBeads(env.store)
			if err != nil {
				t.Fatal(err)
			}
			if len(sessions) != len(result.State) {
				t.Fatalf("invalid fixture: %d session beads for %d desired sessions: %s", len(sessions), len(result.State), env.stderr.String())
			}
			attempts := reconcileSessionBeadsAtPath(context.Background(), cityPath, sessions, result.State, configured,
				env.cfg, env.sp, env.store, nil, nil, nil, nil, env.dt, result.ScaleCheckCounts, false, nil,
				"efficiency-probe", nil, env.clk, env.rec, 0, 0, &env.stdout, &env.stderr, env.startOptions...)
			running, err := env.sp.ListRunning("")
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("cap=%d pool_demand=%d named=%d desired=%d start_attempts=%d fake_running=%d", cap, result.ScaleCheckCounts["worker"], cap, len(result.State), attempts, len(running))
			if len(running) < cap {
				t.Fatalf("invalid fixture: did not fill capacity: running=%d cap=%d: %s", len(running), cap, env.stderr.String())
			}
			if len(running) > cap {
				t.Errorf("workspace cap exceeded: fake_running=%d cap=%d", len(running), cap)
			}
			all, err := loadSessionBeads(env.store)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != len(sessions) {
				t.Fatalf("capacity refusal closed a pending session: before=%d after=%d", len(sessions), len(all))
			}
			for _, b := range all {
				if b.Metadata["wake_attempts"] != "" || b.Metadata["quarantined_until"] != "" {
					t.Fatalf("capacity refusal counted as a crash: %v", b.Metadata)
				}
			}
		})
	}
}

func TestWorkspaceSessionAdmissionManualRuntimeHandle(t *testing.T) {
	env := newReconcilerTestEnv()
	env.cfg.Workspace.MaxActiveSessions = intPtr(1)
	if err := env.sp.Start(context.Background(), "existing", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
	factory, err := workerFactoryWithConfig(t.TempDir(), env.store, env.sp, env.cfg)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := factory.RuntimeHandle("manual", "probe", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.StartResolved(context.Background(), "true", runtime.Config{}); err == nil {
		t.Fatal("manual runtime handle bypassed city capacity")
	}
	if env.sp.IsRunning("manual") || !env.sp.IsRunning("existing") {
		t.Fatal("capacity refusal changed running work")
	}
}

func TestWorkspaceSessionAdmissionPoolControl(t *testing.T) {
	cfg := &config.City{Workspace: config.Workspace{MaxActiveSessions: intPtr(32)}, Agents: []config.Agent{{Name: "worker", MaxActiveSessions: intPtr(64)}}}
	var requests []SessionRequest
	for i := 0; i < 33; i++ {
		requests = append(requests, SessionRequest{Template: "worker", Tier: "new"})
	}
	got := applyNestedCaps(cfg, requests, nil, nil)
	count := 0
	for _, pool := range got {
		count += len(pool.Requests)
	}
	t.Logf("pool requests=33 accepted=%d workspace_cap=32", count)
	if count != 32 {
		t.Fatalf("accepted=%d, want 32", count)
	}
}
