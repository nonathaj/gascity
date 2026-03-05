package jsonl

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
)

func writeLines(t *testing.T, f *os.File, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	f.Sync() //nolint:errcheck
}

func TestWatcherAssistantMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("claude", path)
	defer w.Close() //nolint:errcheck // test cleanup

	// Write after watcher has started (it seeks to end on open).
	writeLines(t, f, `{"type":"assistant","message":"hello world","timestamp":"2025-01-01T00:00:00Z"}`)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventAssistantMessage {
			t.Fatalf("got type %v, want EventAssistantMessage", ev.Type)
		}
		if ev.Agent != "claude" {
			t.Fatalf("got agent %q, want %q", ev.Agent, "claude")
		}
		if ev.Data != "hello world" {
			t.Fatalf("got data %q, want %q", ev.Data, "hello world")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherToolCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	writeLines(t, f, `{"type":"tool_use","tool_name":"Read","timestamp":"2025-01-01T00:00:00Z"}`)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventToolCall {
			t.Fatalf("got type %v, want EventToolCall", ev.Type)
		}
		if ev.Data != "Read" {
			t.Fatalf("got data %q, want %q", ev.Data, "Read")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherToolCallAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	writeLines(t, f, `{"type":"tool_call","tool_name":"Write","timestamp":"2025-01-01T00:00:00Z"}`)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventToolCall {
			t.Fatalf("got type %v, want EventToolCall", ev.Type)
		}
		if ev.Data != "Write" {
			t.Fatalf("got data %q, want %q", ev.Data, "Write")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherToolResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	writeLines(t, f, `{"type":"tool_result","message":"ok","timestamp":"2025-01-01T00:00:00Z"}`)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventToolResult {
			t.Fatalf("got type %v, want EventToolResult", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherThinking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	writeLines(t, f, `{"type":"thinking","timestamp":"2025-01-01T00:00:00Z"}`)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventThinking {
			t.Fatalf("got type %v, want EventThinking", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	writeLines(t, f, `{"type":"error","message":"something broke","timestamp":"2025-01-01T00:00:00Z"}`)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventError {
			t.Fatalf("got type %v, want EventError", ev.Type)
		}
		if ev.Data != "something broke" {
			t.Fatalf("got data %q, want %q", ev.Data, "something broke")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	writeLines(t, f, `{"type":"result","message":"done","timestamp":"2025-01-01T00:00:00Z"}`)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventCompleted {
			t.Fatalf("got type %v, want EventCompleted", ev.Type)
		}
		if ev.Data != "done" {
			t.Fatalf("got data %q, want %q", ev.Data, "done")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherSkipsUnknownType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	// Unknown type followed by known type.
	writeLines(t, f,
		`{"type":"unknown_thing","timestamp":"2025-01-01T00:00:00Z"}`,
		`{"type":"assistant","message":"hi","timestamp":"2025-01-01T00:00:01Z"}`,
	)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventAssistantMessage {
			t.Fatalf("got type %v, want EventAssistantMessage (unknown type should be skipped)", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherSkipsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	writeLines(t, f,
		`not json at all`,
		`{"type":"assistant","message":"ok","timestamp":"2025-01-01T00:00:00Z"}`,
	)

	select {
	case ev := <-w.Events():
		if ev.Type != agent.EventAssistantMessage {
			t.Fatalf("got type %v, want EventAssistantMessage", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherZeroTimestampUsesNow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	before := time.Now()
	writeLines(t, f, `{"type":"assistant","message":"hi"}`)

	select {
	case ev := <-w.Events():
		if ev.Time.Before(before) {
			t.Fatalf("event time %v is before test start %v", ev.Time, before)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestWatcherMultipleEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	writeLines(t, f,
		`{"type":"assistant","message":"a","timestamp":"2025-01-01T00:00:00Z"}`,
		`{"type":"tool_use","tool_name":"Bash","timestamp":"2025-01-01T00:00:01Z"}`,
		`{"type":"result","message":"done","timestamp":"2025-01-01T00:00:02Z"}`,
	)

	expected := []agent.EventType{
		agent.EventAssistantMessage,
		agent.EventToolCall,
		agent.EventCompleted,
	}

	for i, want := range expected {
		select {
		case ev := <-w.Events():
			if ev.Type != want {
				t.Fatalf("event %d: got type %v, want %v", i, ev.Type, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for event %d", i)
		}
	}
}

func TestWatcherCloseStopsChannel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	f.Close() //nolint:errcheck // test cleanup

	w := New("agent1", path)
	w.Close() //nolint:errcheck // test cleanup

	// Channel should close eventually.
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-w.Events():
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("timeout waiting for channel close")
		}
	}
}

func TestWatcherMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.jsonl")

	w := New("agent1", path)
	defer w.Close() //nolint:errcheck // test cleanup

	// Should close the channel without error.
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-w.Events():
			if !ok {
				return
			}
		case <-timer.C:
			t.Fatal("timeout waiting for channel close on missing file")
		}
	}
}
