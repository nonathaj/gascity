package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
	"github.com/gastownhall/gascity/internal/materialize"
	"github.com/gastownhall/gascity/internal/validation"
)

// runStage1SkillMaterialization performs stage-1 skill materialization
// for every eligible agent in cfg. Stage 1 materialises at each
// agent's scope root (city or rig path). Session-worktree
// materialisation (stage 2) is a separate PreStart-based path wired in
// by template_resolve.go via skill_integration.go.
//
// Per engdocs/proposals/skill-materialization.md § "Stage 2 runtime
// gate", only runtimes that execute host-side PreStart participate.
// Stage-1 runs in the supervisor process on the host and populates
// the scope root regardless of where the agent ultimately runs, but
// we gate on the same eligibility check so k8s/acp agents (which
// cannot see the host scope root anyway) don't produce wasted I/O
// or confusing symlink state. Agents whose provider has no vendor
// sink (copilot/cursor/pi/omp in v0.15.1) are skipped.
//
// Per-agent errors are logged to stderr and do not abort the pass —
// the supervisor should continue reconciling every other agent.
// Catalog-load errors are returned so the caller can surface a single
// top-level diagnostic.
func runStage1SkillMaterialization(cityPath string, cfg *config.City, stderr io.Writer) error {
	if cfg == nil {
		return nil
	}
	cityCat, err := materialize.LoadCityCatalog(cfg.PackSkillsDir)
	if err != nil {
		return fmt.Errorf("load city skill catalog: %w", err)
	}

	for i := range cfg.Agents {
		agent := &cfg.Agents[i]
		if !isStage2EligibleSession(cfg.Session.Provider, agent) {
			continue
		}
		provider := effectiveAgentProvider(agent, cfg.Workspace.Provider)
		vendor, ok := materialize.VendorSink(provider)
		if !ok {
			continue
		}

		agentCat, lerr := materialize.LoadAgentCatalog(agent.SkillsDir)
		if lerr != nil {
			fmt.Fprintf(stderr, "gc: stage-1 materialize-skills for agent %q: LoadAgentCatalog %q: %v\n", //nolint:errcheck // best-effort stderr
				agent.QualifiedName(), agent.SkillsDir, lerr)
			// Continue with empty agent catalog rather than skipping the
			// whole materialization — the shared catalog still delivers.
			agentCat = materialize.AgentCatalog{}
		}

		desired := materialize.EffectiveSet(cityCat, agentCat)
		if len(desired) == 0 {
			continue
		}

		// Resolve the agent's scope root to an absolute path. Use the
		// un-canonicalised form here so the materializer writes into
		// the operator-intended location (e.g., /city/rigs/fe even
		// when it's a symlink to /private/city/...). canonicalisation
		// happens at comparison time inside MaterializeAgent via
		// EvalSymlinks, so owner-root matching still works.
		scopeRoot := resolveAgentScopeRoot(agent, cityPath, cfg.Rigs)
		if !filepath.IsAbs(scopeRoot) {
			scopeRoot = filepath.Join(cityPath, scopeRoot)
		}
		sinkDir := filepath.Join(scopeRoot, vendor)

		owned := append([]string{}, cityCat.OwnedRoots...)
		if agentCat.OwnedRoot != "" {
			owned = append(owned, agentCat.OwnedRoot)
		}

		res, merr := materialize.MaterializeAgent(materialize.MaterializeRequest{
			SinkDir:     sinkDir,
			Desired:     desired,
			OwnedRoots:  owned,
			LegacyNames: materialize.LegacyStubNames(),
		})
		if merr != nil {
			fmt.Fprintf(stderr, "gc: stage-1 materialize-skills for agent %q at %s: %v\n", //nolint:errcheck // best-effort stderr
				agent.QualifiedName(), sinkDir, merr)
			continue
		}
		for _, s := range res.Skipped {
			fmt.Fprintf(stderr, "gc: agent %q skipped skill %q at %s — %s\n", //nolint:errcheck // best-effort stderr
				agent.QualifiedName(), s.Name, s.Path, s.Reason)
		}
		for _, w := range res.Warnings {
			fmt.Fprintf(stderr, "gc: agent %q stage-1 materialize warning: %s\n", //nolint:errcheck // best-effort stderr
				agent.QualifiedName(), w)
		}
	}
	return nil
}

// checkSkillCollisions runs the skill-collision validator before
// materialisation. Two agents sharing the same (scope-root, vendor)
// sink cannot both provide an agent-local skill under the same name
// — one of them would overwrite the other's symlink with a different
// target. Returns a formatted error suitable for direct display to
// the operator; nil when there are no collisions.
//
// `gc start` uses this as a hard gate (returning an error fails
// start). The supervisor tick runs it on every reconcile and fails
// the tick's materialise step on violation, leaving previously-
// materialised skills in place.
//
// cityPath is used to rewrite the "<city>" sentinel in the formatted
// error to the operator-visible city root.
func checkSkillCollisions(cfg *config.City, cityPath string) error {
	if cfg == nil {
		return nil
	}
	collisions := validation.ValidateSkillCollisions(cfg)
	if len(collisions) == 0 {
		return nil
	}
	return fmt.Errorf("%s", doctor.FormatSkillCollisions(collisions, cityPath))
}
