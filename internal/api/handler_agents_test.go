package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/session"
)

func TestAgentList(t *testing.T) {
	state := newFakeState(t)
	state.sp.Start(context.Background(), "myrig--worker", session.Config{}) //nolint:errcheck
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/agents", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp listResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Total != 1 {
		t.Errorf("Total = %d, want 1", resp.Total)
	}
}

func TestAgentListPoolExpansion(t *testing.T) {
	state := newFakeState(t)
	state.cfg.Agents = []config.Agent{
		{
			Name: "polecat",
			Dir:  "myrig",
			Pool: &config.PoolConfig{Min: 1, Max: 3, Check: "echo 3"},
		},
	}
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/agents", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp struct {
		Items []agentResponse `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Total != 3 {
		t.Fatalf("Total = %d, want 3", resp.Total)
	}

	// Check pool member names.
	want := []string{"myrig/polecat-1", "myrig/polecat-2", "myrig/polecat-3"}
	for i, name := range want {
		if resp.Items[i].Name != name {
			t.Errorf("Items[%d].Name = %q, want %q", i, resp.Items[i].Name, name)
		}
		if resp.Items[i].Pool != "myrig/polecat" {
			t.Errorf("Items[%d].Pool = %q, want %q", i, resp.Items[i].Pool, "myrig/polecat")
		}
	}
}

func TestAgentListFilterByRig(t *testing.T) {
	state := newFakeState(t)
	state.cfg.Agents = []config.Agent{
		{Name: "worker", Dir: "rig1"},
		{Name: "worker", Dir: "rig2"},
	}
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/agents?rig=rig1", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		Items []agentResponse `json:"items"`
		Total int             `json:"total"`
	}
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp.Total != 1 {
		t.Errorf("Total = %d, want 1", resp.Total)
	}
	if resp.Items[0].Name != "rig1/worker" {
		t.Errorf("Name = %q, want %q", resp.Items[0].Name, "rig1/worker")
	}
}

func TestAgentListFilterByRunning(t *testing.T) {
	state := newFakeState(t)
	state.cfg.Agents = []config.Agent{
		{Name: "running-agent"},
		{Name: "stopped-agent"},
	}
	state.sp.Start(context.Background(), "running-agent", session.Config{}) //nolint:errcheck
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/agents?running=true", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp struct {
		Items []agentResponse `json:"items"`
		Total int             `json:"total"`
	}
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp.Total != 1 {
		t.Errorf("Total = %d, want 1", resp.Total)
	}
	if resp.Items[0].Name != "running-agent" {
		t.Errorf("Name = %q, want %q", resp.Items[0].Name, "running-agent")
	}
}

func TestAgentGet(t *testing.T) {
	state := newFakeState(t)
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/agent/myrig/worker", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp agentResponse
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp.Name != "myrig/worker" {
		t.Errorf("Name = %q, want %q", resp.Name, "myrig/worker")
	}
}

func TestAgentGetNotFound(t *testing.T) {
	state := newFakeState(t)
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/agent/nonexistent", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAgentPeek(t *testing.T) {
	state := newFakeState(t)
	state.sp.Start(context.Background(), "myrig--worker", session.Config{}) //nolint:errcheck
	state.sp.SetPeekOutput("myrig--worker", "Hello from agent")
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/agent/myrig/worker/peek", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp map[string]string
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["output"] != "Hello from agent" {
		t.Errorf("output = %q, want %q", resp["output"], "Hello from agent")
	}
}

func TestFindAgentPoolMaxZero(t *testing.T) {
	// Regression: pool with Max=0 should default to 1, matching expandAgent.
	cfg := &config.City{
		Agents: []config.Agent{
			{
				Name: "polecat",
				Dir:  "myrig",
				Pool: &config.PoolConfig{Min: 0, Max: 0, Check: "echo 0"},
			},
		},
	}
	// Max=0 defaults to 1 member, so "polecat" (no suffix) should be found.
	a, ok := findAgent(cfg, "myrig/polecat")
	if !ok {
		t.Fatal("findAgent(myrig/polecat) = false, want true for pool with Max=0")
	}
	if a.Name != "polecat" {
		t.Errorf("agent.Name = %q, want %q", a.Name, "polecat")
	}
}

func TestAgentPeekNotRunning(t *testing.T) {
	state := newFakeState(t)
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/agent/myrig/worker/peek", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAgentSuspendResume(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	// Suspend.
	req := newPostRequest("/v0/agent/myrig/worker/suspend", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("suspend: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !state.suspended["myrig/worker"] {
		t.Error("agent not suspended")
	}

	// Resume.
	req = newPostRequest("/v0/agent/myrig/worker/resume", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("resume: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if state.suspended["myrig/worker"] {
		t.Error("agent still suspended after resume")
	}
}

func TestAgentKill(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	req := newPostRequest("/v0/agent/myrig/worker/kill", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("kill: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !state.killed["myrig/worker"] {
		t.Error("agent not killed")
	}
}

func TestAgentNudge(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	body := strings.NewReader(`{"message":"wake up"}`)
	req := newPostRequest("/v0/agent/myrig/worker/nudge", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("nudge: status = %d, want %d", rec.Code, http.StatusOK)
	}
	if state.nudges["myrig/worker"] != "wake up" {
		t.Errorf("nudge message = %q, want %q", state.nudges["myrig/worker"], "wake up")
	}
}

func TestAgentActionNotFound(t *testing.T) {
	state := newFakeMutatorState(t)
	srv := New(state)

	req := newPostRequest("/v0/agent/nonexistent/suspend", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAgentActionNotMutator(t *testing.T) {
	// fakeState (not fakeMutatorState) doesn't implement StateMutator.
	state := newFakeState(t)
	srv := New(state)

	req := newPostRequest("/v0/agent/myrig/worker/suspend", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
}
