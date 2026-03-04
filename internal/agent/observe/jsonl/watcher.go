// Package jsonl provides a Claude JSONL session file observer.
//
// Claude writes structured events to ~/.claude/projects/*/session.jsonl.
// This observer tails those files and emits [agent.Event] values,
// providing structured observation independent of the execution runtime.
package jsonl

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"sync"
	"time"

	"github.com/julianknutsen/gascity/internal/agent"
)

// jsonlEntry is the minimal shape of a Claude JSONL session entry.
// Only fields needed for event classification are decoded.
type jsonlEntry struct {
	Type      string    `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	Message   string    `json:"message,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
}

// Watcher tails a Claude JSONL session file and emits Events.
// It reads from the current end of file forward, so historical entries
// are not replayed.
type Watcher struct {
	agentName string
	ch        chan agent.Event
	done      chan struct{}
	closeOnce sync.Once
}

// New creates a Watcher that tails the given JSONL file path.
// The file is opened and seeked to end synchronously so that all
// writes after New returns are observed. The watcher starts a
// background goroutine for polling. Call Close to stop it.
func New(agentName, path string) *Watcher {
	w := &Watcher{
		agentName: agentName,
		ch:        make(chan agent.Event, 64),
		done:      make(chan struct{}),
	}

	f, err := os.Open(path)
	if err != nil {
		// File doesn't exist yet — close channel, no observation.
		close(w.ch)
		return w
	}

	// Seek to end — only observe new entries.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close() //nolint:errcheck // best-effort
		close(w.ch)
		return w
	}

	go w.tail(f)
	return w
}

// Events returns the channel of agent events.
func (w *Watcher) Events() <-chan agent.Event {
	return w.ch
}

// Close stops the watcher and closes the Events channel.
func (w *Watcher) Close() error {
	w.closeOnce.Do(func() {
		close(w.done)
	})
	return nil
}

func (w *Watcher) tail(f *os.File) {
	defer close(w.ch)
	defer f.Close() //nolint:errcheck // best-effort

	const pollInterval = 500 * time.Millisecond

	reader := bufio.NewReader(f)
	for {
		select {
		case <-w.done:
			return
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			// EOF — wait and retry.
			select {
			case <-w.done:
				return
			case <-time.After(pollInterval):
				continue
			}
		}

		ev, ok := w.parseLine(line)
		if !ok {
			continue
		}

		select {
		case w.ch <- ev:
		case <-w.done:
			return
		}
	}
}

func (w *Watcher) parseLine(line []byte) (agent.Event, bool) {
	var entry jsonlEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return agent.Event{}, false
	}

	ts := entry.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	ev := agent.Event{
		Time:  ts,
		Agent: w.agentName,
	}

	switch entry.Type {
	case "assistant":
		ev.Type = agent.EventAssistantMessage
		ev.Data = entry.Message
	case "tool_use", "tool_call":
		ev.Type = agent.EventToolCall
		ev.Data = entry.ToolName
	case "tool_result":
		ev.Type = agent.EventToolResult
		ev.Data = entry.Message
	case "thinking":
		ev.Type = agent.EventThinking
	case "error":
		ev.Type = agent.EventError
		ev.Data = entry.Message
	case "result":
		ev.Type = agent.EventCompleted
		ev.Data = entry.Message
	default:
		return agent.Event{}, false
	}

	return ev, true
}
