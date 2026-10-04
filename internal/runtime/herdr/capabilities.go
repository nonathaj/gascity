package herdr

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/runtime"
)

// Optional capability interfaces herdr supports natively. (Relaunch,
// ProcessTableScanner, InterruptBoundaryWait, and DialogProvider are
// deliberately omitted from the first cut — the reconciler degrades gracefully
// when a provider lacks them: Relaunch falls back to Stop+Start, the others to
// no-op/default behavior.) SessionEventProvider is implemented in events.go
// over the socket API's events.subscribe stream.
var (
	_ runtime.IdleWaitProvider       = (*Provider)(nil)
	_ runtime.ImmediateNudgeProvider = (*Provider)(nil)
	// NudgeReadinessProvider lets the queued-nudge gate read the agent's state
	// directly instead of waiting for "idle" (see NudgeReadiness below).
	_ runtime.NudgeReadinessProvider = (*Provider)(nil)
	// LivenessObserver lets the reconciler read aliveness from herdr's own
	// agent-status instead of the host process-table walk (see
	// provider.go ObserveLiveness).
	_ runtime.LivenessObserver = (*Provider)(nil)
	// SessionEventProvider is implemented in events.go over the socket API's
	// events.subscribe stream (see #4217 herdr-first-class).
	_ runtime.SessionEventProvider = (*Provider)(nil)
)

// idleWaitOutcome is the legible verdict of one `agent wait --until idle`
// probe. The interface-facing WaitForIdle keeps its proceed-either-way
// contract, but internal callers (Start's startup delivery) need to see WHY a
// wait did not confirm: the live city measured 0 ok / 8 timeout / 2 error
// over 20h with every verdict silently discarded (gas-90h).
type idleWaitOutcome string

const (
	idleWaitReached idleWaitOutcome = "idle"     // herdr observed the agent idle
	idleWaitTimeout idleWaitOutcome = "timeout"  // bound elapsed without idle
	idleWaitNoAgent idleWaitOutcome = "no_agent" // no registered agent (raw shell pane)
	idleWaitError   idleWaitOutcome = "error"    // transport or unexpected herdr error
)

// waitForIdleOutcome blocks until herdr reports the agent idle or the timeout
// elapses, via herdr's native `agent wait --until idle` (the ≥0.7.5 flag
// spelling) — vs the pane-polling tmux does — and returns the verdict
// distinctly instead of discarding it.
func (p *Provider) waitForIdleOutcome(ctx context.Context, name string, timeout time.Duration) idleWaitOutcome {
	ms := int(timeout / time.Millisecond)
	if ms < 1 {
		ms = 1
	}
	_, err := p.c.run(ctx, "agent", "wait", herdrAgentName(name), "--until", "idle", "--timeout", strconv.Itoa(ms))
	switch {
	case err == nil:
		return idleWaitReached
	case herdrErrorCode(err) == "timeout":
		return idleWaitTimeout
	case isAgentNotFound(err):
		return idleWaitNoAgent
	default:
		return idleWaitError
	}
}

// WaitForIdle blocks until herdr reports the agent idle or the timeout
// elapses. Either outcome (idle reached or timed out) means the caller may
// proceed — as does an unregistered session (raw shell panes have no agent to
// wait on) — so only context cancellation surfaces as an error; the timeout
// is a hard bound.
func (p *Provider) WaitForIdle(ctx context.Context, name string, timeout time.Duration) error {
	_ = p.waitForIdleOutcome(ctx, name, timeout)
	return ctx.Err()
}

// NudgeReadiness reports whether the agent is in a state to take queued input,
// from one `agent get`. There is no wait: the answer is herdr's agent_status
// at this moment.
//
// It is what the queued-nudge gate asks instead of WaitForIdle. A session
// parked at its prompt after a finished turn reads "done" in herdr, never
// "idle", so `agent wait --until idle` times out on it and a nudge queued to a
// parked session was never delivered (gf-pdx7a8).
//
// An error means herdr could not be asked (the server is down, the reply did
// not decode). It is not a verdict; in particular it is not "ready".
func (p *Provider) NudgeReadiness(ctx context.Context, name string) (runtime.NudgeReadiness, string, error) {
	info, present, err := p.c.getAgent(ctx, herdrAgentName(name))
	if err != nil {
		return "", "", err
	}
	if !present {
		return runtime.NudgeNoAgent, "", nil
	}
	status := strings.ToLower(strings.TrimSpace(info.AgentStatus))
	return nudgeReadinessFromStatus(status), status, nil
}

// nudgeReadinessFromStatus maps herdr's agent_status to a readiness.
//
//	idle     the prompt is rendered and the input box is empty   ready
//	done     a turn finished; the agent is back at its prompt    ready
//	working  a turn is running                                   busy
//	blocked  a dialog is waiting for an answer                   blocked
//	unknown  herdr cannot classify the pane                      unknown
//
// Anything else is unknown too, including a status a later herdr adds: a new
// state holds queued input until someone decides what it means, rather than
// being pasted into on a guess.
func nudgeReadinessFromStatus(status string) runtime.NudgeReadiness {
	switch status {
	case agentStateIdle, agentStatusDone:
		return runtime.NudgeReady
	case agentStatusWorking:
		return runtime.NudgeBusy
	case agentStatusBlocked:
		return runtime.NudgeBlocked
	default:
		return runtime.NudgeUnclassified
	}
}

// NudgeNow injects input immediately. herdr's send/run already deliver without a
// wait-idle heuristic, so this is the same delivery path as Nudge.
func (p *Provider) NudgeNow(name string, content []runtime.ContentBlock) error {
	return p.Nudge(name, content)
}
