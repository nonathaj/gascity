package auto

import (
	"context"
	"testing"

	"github.com/gastownhall/gascity/internal/session"
)

var _ session.Provider = (*Provider)(nil)

func TestRouteDefaultAndACP(t *testing.T) {
	defaultSP := session.NewFake()
	acpSP := session.NewFake()
	p := New(defaultSP, acpSP)

	// Unregistered session routes to default.
	if got := p.route("agent-a"); got != defaultSP {
		t.Fatal("unregistered session should route to default")
	}

	// Register as ACP.
	p.RouteACP("agent-b")
	if got := p.route("agent-b"); got != acpSP {
		t.Fatal("registered session should route to ACP")
	}
	if got := p.route("agent-a"); got != defaultSP {
		t.Fatal("other session should still route to default")
	}
}

func TestUnroute(t *testing.T) {
	defaultSP := session.NewFake()
	acpSP := session.NewFake()
	p := New(defaultSP, acpSP)

	p.RouteACP("agent-x")
	if got := p.route("agent-x"); got != acpSP {
		t.Fatal("should route to ACP after registration")
	}

	p.Unroute("agent-x")
	if got := p.route("agent-x"); got != defaultSP {
		t.Fatal("should route to default after unroute")
	}
}

func TestAttachReturnsErrorForACP(t *testing.T) {
	defaultSP := session.NewFake()
	acpSP := session.NewFake()
	p := New(defaultSP, acpSP)

	p.RouteACP("headless-agent")
	err := p.Attach("headless-agent")
	if err == nil {
		t.Fatal("Attach on ACP session should return error")
	}
	if want := `agent "headless-agent" uses ACP transport (no terminal to attach to)`; err.Error() != want {
		t.Errorf("Attach error = %q, want %q", err.Error(), want)
	}

	// Default sessions with an existing session should not error.
	_ = defaultSP.Start(context.Background(), "normal-agent", session.Config{})
	if err := p.Attach("normal-agent"); err != nil {
		t.Errorf("Attach on default session should not error: %v", err)
	}
}

func TestListRunningMergesBothBackends(t *testing.T) {
	defaultSP := session.NewFake()
	acpSP := session.NewFake()
	p := New(defaultSP, acpSP)

	// Start sessions on each backend.
	_ = defaultSP.Start(context.Background(), "default-1", session.Config{})
	_ = acpSP.Start(context.Background(), "acp-1", session.Config{})

	names, err := p.ListRunning("")
	if err != nil {
		t.Fatalf("ListRunning: %v", err)
	}
	if len(names) != 2 {
		t.Fatalf("ListRunning returned %d names, want 2: %v", len(names), names)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["default-1"] || !found["acp-1"] {
		t.Errorf("ListRunning = %v, want default-1 and acp-1", names)
	}
}

func TestStopCleansUpRoute(t *testing.T) {
	defaultSP := session.NewFake()
	acpSP := session.NewFake()
	p := New(defaultSP, acpSP)

	p.RouteACP("agent-z")
	_ = acpSP.Start(context.Background(), "agent-z", session.Config{})

	if err := p.Stop("agent-z"); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After stop, route entry should be cleaned up.
	if got := p.route("agent-z"); got != defaultSP {
		t.Fatal("route should fall back to default after Stop")
	}
}
