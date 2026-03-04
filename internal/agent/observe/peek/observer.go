// Package peek provides a fallback ObservationStrategy that wraps
// terminal scraping (Peek) as structured [agent.Event] values.
//
// This is the degraded observation path — same data as raw terminal
// output, but in the unified event shape. Every agent has at least
// this observer available, regardless of runtime.
package peek

import (
	"sync"
	"time"

	"github.com/julianknutsen/gascity/internal/agent"
)

// Peeker is the subset of agent.Agent needed by the observer.
type Peeker interface {
	Peek(lines int) (string, error)
}

// Observer polls Peek() and emits EventOutput events when new output
// appears. It compares consecutive snapshots and only emits when the
// content changes.
type Observer struct {
	agentName string
	peeker    Peeker
	lines     int
	ch        chan agent.Event
	done      chan struct{}
	closeOnce sync.Once
}

// New creates an Observer that polls the given Peeker every pollInterval.
// The observer starts a background goroutine immediately.
// Call Close to stop it.
func New(agentName string, peeker Peeker, lines int) *Observer {
	if lines <= 0 {
		lines = 50
	}
	o := &Observer{
		agentName: agentName,
		peeker:    peeker,
		lines:     lines,
		ch:        make(chan agent.Event, 64),
		done:      make(chan struct{}),
	}
	go o.run()
	return o
}

// Events returns the channel of agent events.
func (o *Observer) Events() <-chan agent.Event {
	return o.ch
}

// Close stops the observer and closes the Events channel.
func (o *Observer) Close() error {
	o.closeOnce.Do(func() {
		close(o.done)
	})
	return nil
}

func (o *Observer) run() {
	defer close(o.ch)

	const pollInterval = 500 * time.Millisecond

	var lastSnapshot string

	for {
		select {
		case <-o.done:
			return
		case <-time.After(pollInterval):
		}

		snapshot, err := o.peeker.Peek(o.lines)
		if err != nil || snapshot == lastSnapshot {
			continue
		}
		lastSnapshot = snapshot

		ev := agent.Event{
			Time:  time.Now(),
			Type:  agent.EventOutput,
			Agent: o.agentName,
			Data:  snapshot,
		}

		select {
		case o.ch <- ev:
		case <-o.done:
			return
		}
	}
}
