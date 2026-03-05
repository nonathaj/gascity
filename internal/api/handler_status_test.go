package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gastownhall/gascity/internal/session"
)

func TestHandleStatus(t *testing.T) {
	state := newFakeState(t)
	// Start a fake session so Running > 0.
	state.sp.Start(context.Background(), "myrig--worker", session.Config{}) //nolint:errcheck
	srv := New(state)

	req := httptest.NewRequest("GET", "/v0/status", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Name != "test-city" {
		t.Errorf("Name = %q, want %q", resp.Name, "test-city")
	}
	if resp.AgentCount != 1 {
		t.Errorf("AgentCount = %d, want 1", resp.AgentCount)
	}
	if resp.RigCount != 1 {
		t.Errorf("RigCount = %d, want 1", resp.RigCount)
	}
	if resp.Running != 1 {
		t.Errorf("Running = %d, want 1", resp.Running)
	}

	// Check X-GC-Index header is present.
	if rec.Header().Get("X-GC-Index") == "" {
		t.Error("missing X-GC-Index header")
	}
}

func TestHandleHealth(t *testing.T) {
	state := newFakeState(t)
	srv := New(state)

	req := httptest.NewRequest("GET", "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
