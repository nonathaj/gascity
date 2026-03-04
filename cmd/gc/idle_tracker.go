package main

import (
	"time"

	"github.com/julianknutsen/gascity/internal/agent"
)

// idleTracker checks for agents that have been idle longer than their
// configured timeout. Nil means idle checking is disabled (backward
// compatible). Follows the same nil-guard pattern as crashTracker.
type idleTracker interface {
	// checkIdle returns true if the agent has been idle longer than its
	// configured timeout. Queries agent.GetLastActivity().
	checkIdle(a agent.Agent, now time.Time) bool

	// setTimeout configures the idle timeout for a session name.
	// Called during agent list construction. Duration of 0 disables.
	setTimeout(sessionName string, timeout time.Duration)
}

// memoryIdleTracker is the production implementation of idleTracker.
type memoryIdleTracker struct {
	timeouts map[string]time.Duration // session → idle timeout
}

// newIdleTracker creates an idle tracker. Returns nil if disabled.
// Callers check for nil before using.
func newIdleTracker() *memoryIdleTracker {
	return &memoryIdleTracker{
		timeouts: make(map[string]time.Duration),
	}
}

func (m *memoryIdleTracker) setTimeout(sessionName string, timeout time.Duration) {
	if timeout <= 0 {
		delete(m.timeouts, sessionName)
		return
	}
	m.timeouts[sessionName] = timeout
}

func (m *memoryIdleTracker) checkIdle(a agent.Agent, now time.Time) bool {
	timeout, ok := m.timeouts[a.SessionName()]
	if !ok || timeout <= 0 {
		return false
	}
	lastActivity, err := a.GetLastActivity()
	if err != nil || lastActivity.IsZero() {
		return false // don't false-positive on error or unsupported
	}
	return now.Sub(lastActivity) > timeout
}
