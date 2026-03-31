package main

import (
	"strings"
	"time"
)

// AwakeInput contains all pre-computed state needed to decide which sessions
// should be awake. All external I/O (shell commands, tmux checks, store
// queries) happens before this function is called.
type AwakeInput struct {
	Agents           []AwakeAgent
	NamedSessions    []AwakeNamedSession
	SessionBeads     []AwakeSessionBead
	WorkBeads        []AwakeWorkBead
	ScaleCheckCounts map[string]int  // agent template → desired count
	RunningSessions  map[string]bool // session name → tmux exists
	AttachedSessions map[string]bool // session name → user attached
	PendingSessions  map[string]bool // session name → pending interaction
	Now              time.Time
}

// AwakeAgent represents an [[agent]] config entry.
type AwakeAgent struct {
	QualifiedName string   // e.g. "hello-world/polecat"
	DependsOn     []string // template names this agent depends on
	Suspended     bool
}

// AwakeNamedSession represents a [[named_session]] config entry.
type AwakeNamedSession struct {
	Identity string // qualified name, e.g. "hello-world/refinery"
	Template string // agent template name
	Mode     string // "always" or "on_demand"
}

// AwakeSessionBead represents an open session bead from the store.
type AwakeSessionBead struct {
	ID               string
	SessionName      string
	Template         string
	State            string // "creating", "active", "asleep"
	ManualSession    bool
	NamedIdentity    string // non-empty for named session beads
	QuarantinedUntil time.Time
}

// AwakeWorkBead represents a work bead with an assignee.
type AwakeWorkBead struct {
	ID       string
	Assignee string
	Status   string // "open", "in_progress"
}

// AwakeDecision is the output for a single session.
type AwakeDecision struct {
	ShouldWake bool
	Reason     string // human-readable reason for debugging
}

// ComputeAwakeSet determines which sessions should be awake.
//
// This is a pure function with no side effects. The algorithm:
//  1. Build desired set from config + demand signals
//  2. Any session in desired set should wake
//  3. Attached or pending sessions wake regardless of desired
//  4. Quarantine suppresses wake
//  5. Dependency gate: don't wake if dependencies aren't running
func ComputeAwakeSet(input AwakeInput) map[string]AwakeDecision {
	// Build agent index
	agentsByName := make(map[string]AwakeAgent, len(input.Agents))
	for _, a := range input.Agents {
		agentsByName[a.QualifiedName] = a
	}

	// Step 1: Build desired set
	desired := make(map[string]string) // sessionName → reason

	// Named sessions
	for _, ns := range input.NamedSessions {
		switch ns.Mode {
		case "always":
			// Find or expect a bead for this named session
			if sn := findNamedSessionName(input.SessionBeads, ns.Identity); sn != "" {
				desired[sn] = "named-always"
			} else {
				// No bead yet — will need to be created. Use identity as placeholder.
				desired[ns.Identity] = "named-always"
			}
		case "on_demand":
			// Check if any work bead is assigned to this named session's identity
			if hasAssignedWork(input.WorkBeads, ns.Identity) {
				if sn := findNamedSessionName(input.SessionBeads, ns.Identity); sn != "" {
					desired[sn] = "named-on-demand:assignee"
				} else {
					desired[ns.Identity] = "named-on-demand:assignee"
				}
			}
		}
	}

	// Agent templates (scaled) — use scaleCheckCounts
	for template, count := range input.ScaleCheckCounts {
		if count <= 0 {
			continue
		}
		agent, ok := agentsByName[template]
		if !ok || agent.Suspended {
			continue
		}
		// Collect existing active/creating beads for this template
		active := collectActiveBeads(input.SessionBeads, template)
		// Fill up to count: existing active first, then need new
		for i, bead := range active {
			if i >= count {
				break
			}
			desired[bead.SessionName] = "scaled:demand"
		}
		// If we need more than we have active, remaining slots need new beads
		// (handled by syncSessionBeads, not here — we just mark existing ones)
		// But creating beads also count:
		creating := collectCreatingBeads(input.SessionBeads, template)
		filled := len(active)
		for _, bead := range creating {
			if filled >= count {
				break
			}
			desired[bead.SessionName] = "scaled:creating"
			filled++
		}
	}

	// Manual sessions — always desired if template matches an agent
	for _, bead := range input.SessionBeads {
		if !bead.ManualSession || bead.State == "closed" {
			continue
		}
		if _, ok := agentsByName[bead.Template]; ok {
			desired[bead.SessionName] = "manual"
		}
	}

	// Step 2 + 3: Decide awake
	result := make(map[string]AwakeDecision)

	// All session beads get a decision
	for _, bead := range input.SessionBeads {
		name := bead.SessionName
		decision := AwakeDecision{}

		// Check desired set
		if reason, inDesired := desired[name]; inDesired {
			decision.ShouldWake = true
			decision.Reason = reason
		}

		// Attached override (even if not in desired)
		if input.AttachedSessions[name] {
			decision.ShouldWake = true
			decision.Reason = "attached"
		}

		// Pending interaction override
		if input.PendingSessions[name] {
			decision.ShouldWake = true
			decision.Reason = "pending"
		}

		// Step 3: Quarantine suppression
		if !bead.QuarantinedUntil.IsZero() && input.Now.Before(bead.QuarantinedUntil) {
			decision.ShouldWake = false
			decision.Reason = "quarantined"
		}

		// Step 4: Dependency gate
		if decision.ShouldWake {
			agent, ok := agentsByName[bead.Template]
			if ok && len(agent.DependsOn) > 0 {
				for _, dep := range agent.DependsOn {
					depRunning := false
					for _, other := range input.SessionBeads {
						if other.Template == dep && input.RunningSessions[other.SessionName] {
							depRunning = true
							break
						}
					}
					if !depRunning {
						decision.ShouldWake = false
						decision.Reason = "dependency-not-running:" + dep
						break
					}
				}
			}
		}

		result[name] = decision
	}

	return result
}

// findNamedSessionName finds the session name for a named session identity.
func findNamedSessionName(beads []AwakeSessionBead, identity string) string {
	for _, b := range beads {
		if b.NamedIdentity == identity {
			return b.SessionName
		}
	}
	return ""
}

// hasAssignedWork checks if any work bead is assigned to the given identity.
func hasAssignedWork(workBeads []AwakeWorkBead, identity string) bool {
	for _, wb := range workBeads {
		if strings.TrimSpace(wb.Assignee) == identity &&
			(wb.Status == "open" || wb.Status == "in_progress") {
			return true
		}
	}
	return false
}

// collectActiveBeads returns session beads for a template that are active.
func collectActiveBeads(beads []AwakeSessionBead, template string) []AwakeSessionBead {
	var result []AwakeSessionBead
	for _, b := range beads {
		if b.Template == template && b.State == "active" && !b.ManualSession {
			result = append(result, b)
		}
	}
	return result
}

// collectCreatingBeads returns session beads for a template in creating state.
func collectCreatingBeads(beads []AwakeSessionBead, template string) []AwakeSessionBead {
	var result []AwakeSessionBead
	for _, b := range beads {
		if b.Template == template && b.State == "creating" && !b.ManualSession {
			result = append(result, b)
		}
	}
	return result
}
