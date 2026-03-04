package agent

import "time"

// Event represents a structured observation from an agent's session.
// Events are produced by an ObservationStrategy independently of the
// execution runtime (tmux, Docker, K8s, subprocess).
type Event struct {
	// Time is when the event occurred.
	Time time.Time

	// Type categorizes the event.
	Type EventType

	// Agent is the agent name that produced this event.
	Agent string

	// Data carries type-specific payload (e.g., message text, tool name).
	Data any
}

// EventType classifies agent observation events.
type EventType int

const (
	// EventAssistantMessage is emitted when the agent produces a text response.
	EventAssistantMessage EventType = iota

	// EventToolCall is emitted when the agent invokes a tool.
	EventToolCall

	// EventToolResult is emitted when a tool returns a result.
	EventToolResult

	// EventThinking is emitted when the agent enters a thinking/reasoning phase.
	EventThinking

	// EventError is emitted when an error occurs in the agent session.
	EventError

	// EventIdle is emitted when the agent appears idle (no recent activity).
	EventIdle

	// EventCompleted is emitted when the agent finishes its task.
	EventCompleted

	// EventOutput is emitted by the peek-based fallback observer for
	// raw terminal output. Less structured than other event types.
	EventOutput
)

// ObservationStrategy provides structured agent observation independent
// of the execution runtime. A JSONL observer reads Claude's session files
// on the host regardless of whether Claude runs in tmux, Docker, or K8s.
// A peek-based fallback wraps terminal scraping as events.
type ObservationStrategy interface {
	// Events returns a channel of agent events. The channel is closed
	// when the observer is stopped via Close. Returns nil if no events
	// are available.
	Events() <-chan Event

	// Close stops the observer and releases resources. The Events
	// channel is closed after Close returns.
	Close() error
}
