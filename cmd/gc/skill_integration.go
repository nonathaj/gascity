package main

import (
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/materialize"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/shellquote"
)

// isStage2EligibleSession reports whether skill materialization should
// run for the given agent's session runtime. Per the skill-
// materialization spec (§ "Stage 2 runtime gate"):
//
//	subprocess, tmux → eligible (PreStart runs locally before the
//	                   subprocess spawn / on the host)
//	acp               → ineligible (out of scope for v0.15.1)
//	k8s               → ineligible (PreStart runs inside the pod; the
//	                   gc binary and host skill paths aren't available
//	                   there)
//
// Agent.Session == "acp" overrides the city-level session selector at
// the per-agent level — even in a tmux city, an ACP agent is
// ineligible. Unknown session providers (fake, fail, hybrid,
// exec:<script>) are treated conservatively as ineligible so we never
// inject host-side commands into a runtime whose semantics we haven't
// verified.
func isStage2EligibleSession(citySessionProvider string, agent *config.Agent) bool {
	if agent == nil {
		return false
	}
	if agent.Session == "acp" {
		return false
	}
	switch strings.TrimSpace(citySessionProvider) {
	case "", "tmux", "subprocess":
		return true
	default:
		// k8s, acp, fake, fail, hybrid, exec:<script>, ... — all
		// conservatively ineligible until individually verified.
		return false
	}
}

// agentScopeRoot returns the absolute filesystem root into which stage-1
// materialization writes for this agent. City-scoped agents resolve to
// cityPath; rig-scoped agents resolve to the rig's configured Path
// (looked up by agent.Dir). Per spec, empty scope defaults to "rig".
//
// When the agent is rig-scoped but no matching rig exists in the
// config (e.g., an inline [[agent]] with a bespoke dir), the path
// returned is the agent.Dir string joined onto cityPath when relative.
// Callers should treat the return value as a best-effort identifier;
// it's used for Stage 1 vs Stage 2 discrimination, not as a safety
// boundary.
func agentScopeRoot(agent *config.Agent, cityPath string, rigs []config.Rig) string {
	if agent == nil {
		return cityPath
	}
	scope := agent.Scope
	if scope == "" {
		scope = "rig"
	}
	if scope == "city" {
		return cityPath
	}
	for _, r := range rigs {
		if r.Name == agent.Dir && r.Path != "" {
			return r.Path
		}
	}
	return cityPath
}

// effectiveSkillsForAgent returns the post-precedence desired skill set
// for one agent plus the owned-root prefix list suitable for a
// MaterializeAgent call. Returns (nil, nil) when the city catalog is
// unavailable or when the agent's provider has no vendor sink.
//
// Kept separate from the materializer's own EffectiveSet/LoadAgentCatalog
// so the BuildDesiredState integration can short-circuit when no
// catalog loaded and avoid I/O per agent when the agent has no local
// skills.
func effectiveSkillsForAgent(city *materialize.CityCatalog, agent *config.Agent) ([]materialize.SkillEntry, []string) {
	if agent == nil {
		return nil, nil
	}
	if _, ok := materialize.VendorSink(agent.Provider); !ok {
		return nil, nil
	}

	var agentCat materialize.AgentCatalog
	if agent.SkillsDir != "" {
		// LoadAgentCatalog returns an error only on stat/read failure;
		// treat those as an empty agent catalog (non-fatal) so the
		// shared catalog still drives fingerprint drift.
		if c, err := materialize.LoadAgentCatalog(agent.SkillsDir); err == nil {
			agentCat = c
		}
	}

	sharedCatalog := materialize.CityCatalog{}
	if city != nil {
		sharedCatalog = *city
	}
	desired := materialize.EffectiveSet(sharedCatalog, agentCat)
	if len(desired) == 0 {
		return nil, nil
	}
	owned := append([]string{}, sharedCatalog.OwnedRoots...)
	if agentCat.OwnedRoot != "" {
		owned = append(owned, agentCat.OwnedRoot)
	}
	return desired, owned
}

// mergeSkillFingerprintEntries adds one "skills:<name>" → content-hash
// entry to fpExtra for each desired skill. Hashes use
// runtime.HashPathContent so any byte-level change to a skill's source
// directory triggers a config-fingerprint drift and drains the agent.
//
// Nil-map safe: allocates fpExtra if the caller passed nil. Returns
// the (possibly new) map. The "skills:" prefix partitions the key
// space so entries cannot collide with other fpExtra keys
// (pool.min/pool.max/wake_mode/etc.).
func mergeSkillFingerprintEntries(fpExtra map[string]string, desired []materialize.SkillEntry) map[string]string {
	if len(desired) == 0 {
		return fpExtra
	}
	if fpExtra == nil {
		fpExtra = make(map[string]string, len(desired))
	}
	for _, e := range desired {
		fpExtra["skills:"+e.Name] = runtime.HashPathContent(e.Source)
	}
	return fpExtra
}

// appendMaterializeSkillsPreStart appends a PreStart command that
// invokes `gc internal materialize-skills --agent <name> --workdir
// <path>` for per-session-worktree materialization. The command is
// APPENDED to any existing user-configured PreStart so worktree
// creation and other setup runs first; materialization runs
// immediately before the agent command.
//
// The gc binary path comes from $GC_BIN (populated by the runtime env
// setup) with "gc" as a fallback if the env var isn't available at
// PreStart expansion time. Argument values are shell-quoted.
func appendMaterializeSkillsPreStart(prestart []string, qualifiedName, workDir string) []string {
	cmd := `"${GC_BIN:-gc}" internal materialize-skills --agent ` +
		shellquote.Join([]string{qualifiedName}) + ` --workdir ` + shellquote.Join([]string{workDir})
	return append(prestart, cmd)
}
