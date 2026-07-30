package herdr

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/herdrversion"
	"github.com/gastownhall/gascity/internal/runtime"
)

// TestProviderLive drives the herdr Provider against a real herdr binary in an
// isolated session. Skipped when herdr is unavailable or in -short mode.
//
// The arbitrary shell command below only exercises the legacy launch path
// (herdr ≤ 0.7.4, whose `agent start` registers any command). On herdr ≥ 0.7.5
// the provider launches via pane verbs, which register only processes herdr
// detects as agents — a plain shell loop never registers, so this test targets
// the legacy interface. See TestProviderLivePaneLaunch for the 0.7.5+ path.
func TestProviderLive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live herdr test in -short mode")
	}
	bin, err := exec.LookPath("herdr")
	if err != nil {
		t.Skip("herdr not installed")
	}
	// Only meaningful on the legacy interface; on ≥ 0.7.5 the pane-verb path
	// requires a real agent process (see TestProviderLivePaneLaunch).
	out, _ := exec.Command(bin, "--version").Output()
	if v, perr := herdrversion.Parse(string(out)); perr == nil && !v.SupportsLegacyStart() {
		t.Skipf("herdr %s uses the pane-verb launch; TestProviderLivePaneLaunch covers it", v.Raw)
	}

	p := New("gctest-live", t.TempDir(), t.TempDir(), 0)
	_ = p.Stop("smoke") // clear any leftover from a crashed prior run
	t.Cleanup(func() { _ = p.Stop("smoke"); _ = p.TeardownServer() })

	ctx := context.Background()
	cfg := runtime.Config{
		WorkDir: t.TempDir(),
		Command: `for i in $(seq 1 60); do echo "tick $i"; sleep 1; done`,
		Env:     map[string]string{"GC_SESSION_ID": "gctest-live-session"},
	}
	if err := p.Start(ctx, "smoke", cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Start must persist GC_SESSION_ID to the meta sidecar (tmux parity):
	// ProcessAlive's session-scoped tree-walk widening has nothing to read
	// without it.
	if v, err := p.GetMeta("smoke", "GC_SESSION_ID"); err != nil || v != "gctest-live-session" {
		t.Errorf("GetMeta(GC_SESSION_ID) = %q, %v; want %q, nil", v, err, "gctest-live-session")
	}

	if !p.IsRunning("smoke") {
		t.Error("IsRunning = false after Start")
	}
	if names, err := p.ListRunning("smo"); err != nil || len(names) != 1 || names[0] != "smoke" {
		t.Errorf("ListRunning(smo) = %v, %v; want [smoke]", names, err)
	}

	// Peek the current screen ("visible") — wait for output to render.
	var got string
	for i := 0; i < 20; i++ {
		time.Sleep(300 * time.Millisecond)
		got, _ = p.Peek("smoke", 10)
		if strings.Contains(got, "tick") {
			break
		}
	}
	if !strings.Contains(got, "tick") {
		t.Errorf("Peek did not capture screen output; got %q", got)
	}

	// ProcessAlive: nil → true; matching name → true; bogus → false.
	if !p.ProcessAlive("smoke", nil) {
		t.Error("ProcessAlive(nil) = false")
	}
	if !p.ProcessAlive("smoke", []string{"sleep", "sh", "bash"}) {
		t.Error("ProcessAlive([sleep/sh/bash]) = false")
	}
	if p.ProcessAlive("smoke", []string{"definitely-not-a-real-process"}) {
		t.Error("ProcessAlive([bogus]) = true")
	}

	// Metadata sidecar roundtrip.
	if err := p.SetMeta("smoke", "drain", "1"); err != nil {
		t.Fatalf("SetMeta: %v", err)
	}
	if v, err := p.GetMeta("smoke", "drain"); err != nil || v != "1" {
		t.Errorf("GetMeta(drain) = %q, %v; want 1", v, err)
	}
	if v, err := p.GetMeta("smoke", "absent"); err != nil || v != "" {
		t.Errorf("GetMeta(absent) = %q, %v; want empty,nil", v, err)
	}

	// Stop → no longer running.
	if err := p.Stop("smoke"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	for i := 0; i < 10 && p.IsRunning("smoke"); i++ {
		time.Sleep(200 * time.Millisecond)
	}
	if p.IsRunning("smoke") {
		t.Error("IsRunning = true after Stop")
	}
}

// TestProviderLivePaneLaunch exercises the 0.7.5+ launch path (pane split +
// paste + rename) against a real herdr. It starts an actual agent herdr
// detects (`claude`), because pane-verb registration is detection-driven.
// Skipped when herdr or claude is unavailable, or on the legacy interface.
func TestProviderLivePaneLaunch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live herdr test in -short mode")
	}
	bin, err := exec.LookPath("herdr")
	if err != nil {
		t.Skip("herdr not installed")
	}
	out, _ := exec.Command(bin, "--version").Output()
	if v, perr := herdrversion.Parse(string(out)); perr != nil || v.SupportsLegacyStart() {
		t.Skipf("pane-verb launch only applies to herdr ≥ 0.7.5 (found %q)", strings.TrimSpace(string(out)))
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}

	p := New("gctest-pane", t.TempDir(), t.TempDir(), 0, 0)
	_ = p.Stop("smoke")
	t.Cleanup(func() { _ = p.Stop("smoke"); _ = p.TeardownServer() })

	ctx := context.Background()
	cfg := runtime.Config{
		WorkDir: t.TempDir(),
		Command: "claude",
		Env:     map[string]string{"GC_SESSION_ID": "gctest-pane-session"},
	}
	if err := p.Start(ctx, "smoke", cfg); err != nil {
		t.Fatalf("Start (pane-verb launch): %v", err)
	}
	if v, err := p.GetMeta("smoke", "GC_SESSION_ID"); err != nil || v != "gctest-pane-session" {
		t.Errorf("GetMeta(GC_SESSION_ID) = %q, %v; want %q, nil", v, err, "gctest-pane-session")
	}
	if !p.IsRunning("smoke") {
		t.Error("IsRunning = false after Start")
	}
	if names, err := p.ListRunning("smo"); err != nil || len(names) != 1 || names[0] != "smoke" {
		t.Errorf("ListRunning(smo) = %v, %v; want [smoke]", names, err)
	}
	if !p.ProcessAlive("smoke", nil) {
		t.Error("ProcessAlive(nil) = false")
	}
	if err := p.Stop("smoke"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}
