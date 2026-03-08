package main

import (
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/session"
)

// resolveSessionName returns the bead-derived session name for a qualified
// agent name. When a bead store is available, it looks up (or creates) a
// session bead and returns "s-{beadID}". When no store is available, it
// falls back to the legacy SessionNameFor function.
//
// templateName is the base config template name (e.g., "worker" for pool
// instance "worker-1"). For non-pool agents, templateName == qualifiedName.
//
// Results are cached in p.beadNames for the duration of the build cycle.
func (p *agentBuildParams) resolveSessionName(qualifiedName, templateName string) string {
	// Check cache first.
	if sn, ok := p.beadNames[qualifiedName]; ok {
		return sn
	}

	// No bead store → legacy path.
	if p.beadStore == nil {
		sn := agent.SessionNameFor(p.cityName, qualifiedName, p.sessionTemplate)
		p.beadNames[qualifiedName] = sn
		return sn
	}

	// Look up existing session bead by template label.
	sn := findSessionNameByTemplate(p.beadStore, qualifiedName)
	if sn != "" {
		p.beadNames[qualifiedName] = sn
		return sn
	}

	// No existing bead — create one (auto-create for config agents).
	// Use templateName (base config name) for the template metadata,
	// and qualifiedName (instance name) for common_name/title.
	b, err := p.beadStore.Create(beads.Bead{
		Title: qualifiedName,
		Type:  session.BeadType,
		Labels: []string{
			session.LabelSession,
			"template:" + templateName,
		},
		Metadata: map[string]string{
			"template":    templateName,
			"common_name": qualifiedName,
			"state":       string(session.StateCreating),
		},
	})
	if err != nil {
		fmt.Fprintf(p.stderr, "session bead: creating for %s: %v (falling back to legacy name)\n", qualifiedName, err) //nolint:errcheck
		sn = agent.SessionNameFor(p.cityName, qualifiedName, p.sessionTemplate)
		p.beadNames[qualifiedName] = sn
		return sn
	}

	sn = sessionNameFromBeadID(b.ID)
	if err := p.beadStore.SetMetadata(b.ID, "session_name", sn); err != nil {
		fmt.Fprintf(p.stderr, "session bead: storing session_name for %s: %v\n", qualifiedName, err) //nolint:errcheck
	}
	p.beadNames[qualifiedName] = sn
	return sn
}

// sessionNameFromBeadID derives the tmux session name from a bead ID.
// This is the universal naming convention: "s-" + beadID with "/" replaced.
func sessionNameFromBeadID(beadID string) string {
	return "s-" + strings.ReplaceAll(beadID, "/", "--")
}

// findSessionNameByTemplate searches for an open session bead with the given
// template and returns its session_name metadata. Returns "" if not found.
func findSessionNameByTemplate(store beads.Store, template string) string {
	// Search both session bead types.
	for _, label := range []string{session.LabelSession, sessionBeadLabel} {
		all, err := store.ListByLabel(label, 0)
		if err != nil {
			continue
		}
		for _, b := range all {
			if b.Status == "closed" {
				continue
			}
			if b.Metadata["template"] == template || b.Metadata["common_name"] == template {
				if sn := b.Metadata["session_name"]; sn != "" {
					return sn
				}
			}
		}
	}
	return ""
}

// lookupSessionName resolves a qualified agent name to its bead-derived
// session name by querying the bead store. Returns the session name and
// true if found, or ("", false) if no matching session bead exists.
//
// This is the CLI-facing equivalent of agentBuildParams.resolveSessionName,
// for use by commands that don't go through buildDesiredState.
func lookupSessionName(store beads.Store, qualifiedName string) (string, bool) {
	if store == nil {
		return "", false
	}
	sn := findSessionNameByTemplate(store, qualifiedName)
	if sn != "" {
		return sn, true
	}
	return "", false
}

// lookupSessionNameOrLegacy resolves a qualified agent name to its session
// name. Tries the bead store first; falls back to the legacy SessionNameFor
// function if no bead is found.
func lookupSessionNameOrLegacy(store beads.Store, cityName, qualifiedName, sessionTemplate string) string {
	if sn, ok := lookupSessionName(store, qualifiedName); ok {
		return sn
	}
	return agent.SessionNameFor(cityName, qualifiedName, sessionTemplate)
}
