package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/events"
)

func TestClaudeProjectSlug(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/data/projects/gascity", "-data-projects-gascity"},
		{"/home/user/my.project", "-home-user-my-project"},
		{"/tmp/a/b/c", "-tmp-a-b-c"},
		{"/", "-"},
	}
	for _, tt := range tests {
		got := claudeProjectSlug(tt.path)
		if got != tt.want {
			t.Errorf("claudeProjectSlug(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestFindJSONLSessionFile(t *testing.T) {
	base := t.TempDir()
	slug := "-data-projects-gascity"
	dir := filepath.Join(base, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write two JSONL files with different mod times.
	older := filepath.Join(dir, "old-session.jsonl")
	newer := filepath.Join(dir, "new-session.jsonl")
	if err := os.WriteFile(older, []byte(`{"type":"assistant"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ensure different mod time.
	oldTime := time.Now().Add(-10 * time.Second)
	if err := os.Chtimes(older, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte(`{"type":"tool_use"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := findJSONLSessionFile([]string{base}, "/data/projects/gascity")
	if got != newer {
		t.Errorf("findJSONLSessionFile() = %q, want %q", got, newer)
	}
}

func TestFindJSONLSessionFileNoMatch(t *testing.T) {
	base := t.TempDir()
	got := findJSONLSessionFile([]string{base}, "/no/such/project")
	if got != "" {
		t.Errorf("findJSONLSessionFile() = %q, want empty", got)
	}
}

func TestAgentEventToSystemType(t *testing.T) {
	tests := []struct {
		in   agent.EventType
		want string
	}{
		{agent.EventAssistantMessage, events.AgentMessage},
		{agent.EventToolCall, events.AgentToolCall},
		{agent.EventToolResult, events.AgentToolResult},
		{agent.EventThinking, events.AgentThinking},
		{agent.EventError, events.AgentError},
		{agent.EventIdle, events.AgentIdle},
		{agent.EventCompleted, events.AgentCompleted},
		{agent.EventOutput, events.AgentOutput},
		{agent.EventType(999), ""},
	}
	for _, tt := range tests {
		got := agentEventToSystemType(tt.in)
		if got != tt.want {
			t.Errorf("agentEventToSystemType(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBridgeAgentEvents(t *testing.T) {
	ch := make(chan agent.Event, 3)
	rec := &spyRecorder{}

	ch <- agent.Event{Type: agent.EventToolCall, Data: "Bash"}
	ch <- agent.Event{Type: agent.EventAssistantMessage, Data: "done"}
	ch <- agent.Event{Type: agent.EventType(999)} // unknown — should be skipped
	close(ch)

	bridgeAgentEvents("worker", ch, rec)

	if len(rec.events) != 2 {
		t.Fatalf("got %d events, want 2", len(rec.events))
	}
	if rec.events[0].Type != events.AgentToolCall {
		t.Errorf("events[0].Type = %q, want %q", rec.events[0].Type, events.AgentToolCall)
	}
	if rec.events[0].Actor != "worker" {
		t.Errorf("events[0].Actor = %q, want %q", rec.events[0].Actor, "worker")
	}
	if rec.events[0].Message != "Bash" {
		t.Errorf("events[0].Message = %q, want %q", rec.events[0].Message, "Bash")
	}
	if rec.events[1].Type != events.AgentMessage {
		t.Errorf("events[1].Type = %q, want %q", rec.events[1].Type, events.AgentMessage)
	}
}

func TestBridgeExitsOnChannelClose(t *testing.T) {
	ch := make(chan agent.Event)
	rec := &spyRecorder{}

	done := make(chan struct{})
	go func() {
		bridgeAgentEvents("worker", ch, rec)
		close(done)
	}()

	close(ch)

	select {
	case <-done:
		// OK — goroutine exited.
	case <-time.After(2 * time.Second):
		t.Fatal("bridgeAgentEvents did not exit after channel close")
	}
}

func TestObserveSearchPathsDedup(t *testing.T) {
	paths := observeSearchPaths([]string{
		"/extra/path",
		"/extra/path", // duplicate
		"/another/path",
	})

	// Should include default + deduplicated extras.
	seen := make(map[string]int)
	for _, p := range paths {
		seen[p]++
	}
	for p, count := range seen {
		if count > 1 {
			t.Errorf("path %q appears %d times, want 1", p, count)
		}
	}
	// Extra paths should be present.
	found := false
	for _, p := range paths {
		if p == "/extra/path" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected /extra/path in results: %v", paths)
	}
}

// spyRecorder captures events for testing.
type spyRecorder struct {
	events []events.Event
}

func (s *spyRecorder) Record(e events.Event) {
	s.events = append(s.events, e)
}
