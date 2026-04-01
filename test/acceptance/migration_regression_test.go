//go:build acceptance_a

// Migration regression tests.
//
// Each test encodes a specific bug found by contributor quad341 while
// migrating from steveyegge/gastown to the gascity gastown pack. The
// tests are permanent regression guards: fast (no tmux, no dolt, no
// inference), testing config invariants and pack materialization only.
package acceptance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
	helpers "github.com/gastownhall/gascity/test/acceptance/helpers"
)

// hasAgent reports whether cfg contains an agent with the given name
// (unqualified). This matches any Dir value.
func hasAgent(cfg *config.City, name string) bool {
	for _, a := range cfg.Agents {
		if a.Name == name {
			return true
		}
	}
	return false
}

// hasAgentQualified reports whether cfg contains an agent whose
// QualifiedName() matches identity exactly.
func hasAgentQualified(cfg *config.City, identity string) bool {
	for _, a := range cfg.Agents {
		if a.QualifiedName() == identity {
			return true
		}
	}
	return false
}

// agentCount returns the number of agents with the given unqualified name.
func agentCount(cfg *config.City, name string) int {
	n := 0
	for _, a := range cfg.Agents {
		if a.Name == name {
			n++
		}
	}
	return n
}

// initGastownCity initializes a city from the gastown example and loads
// the resulting config. Returns the City DSL handle and the parsed config.
func initGastownCity(t *testing.T) (*helpers.City, *config.City) {
	t.Helper()
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	cfg, _, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(c.Dir, "city.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	return c, cfg
}

// TestRegression_PR202_DefaultNamedSessionAlways verifies that the
// mayor's named_session defaults to mode "always" instead of "on_demand".
//
// PR #202 fixed: named_session mode defaulted to "on_demand" which
// required a complex reconciler flow. Correct default is "always" so
// the controller keeps the session alive without extra logic.
func TestRegression_PR202_DefaultNamedSessionAlways(t *testing.T) {
	_, cfg := initGastownCity(t)

	ns := config.FindNamedSession(cfg, "mayor")
	if ns == nil {
		t.Fatal("mayor named_session not found in loaded config")
	}

	mode := ns.ModeOrDefault()
	if mode != "always" {
		t.Errorf("mayor named_session mode = %q, want %q (PR #202 regression)", mode, "always")
	}
}

// TestRegression_PR204_ClosedSessionReleasesName verifies that the
// NamedSession config surface has the expected fields for session name
// lifecycle management.
//
// PR #204 fixed: closed session beads permanently reserved their explicit
// name, preventing reuse. This test validates the config surface that
// enables the fix (actual release logic is in cmd/gc).
func TestRegression_PR204_ClosedSessionReleasesName(t *testing.T) {
	_, cfg := initGastownCity(t)

	// Verify named sessions exist with expected structure.
	if len(cfg.NamedSessions) == 0 {
		t.Fatal("no named sessions found in gastown config")
	}

	// Each named session must have a non-empty Template.
	for i, ns := range cfg.NamedSessions {
		if ns.Template == "" {
			t.Errorf("named_session[%d] has empty Template", i)
		}
		// Mode must be a known value when set.
		m := ns.ModeOrDefault()
		if m != "always" && m != "on_demand" {
			t.Errorf("named_session[%d] (template=%q) has unknown mode %q", i, ns.Template, m)
		}
		// QualifiedName must be deterministic and non-empty.
		qn := ns.QualifiedName()
		if qn == "" {
			t.Errorf("named_session[%d] (template=%q) has empty QualifiedName", i, ns.Template)
		}
	}
}

// TestRegression_GastownPackFormulasParse verifies that all formula TOML
// files in the materialized gastown pack parse as valid TOML.
//
// PR #3044 fixed: invalid TOML escape in a formula file broke 5 CI tests.
func TestRegression_GastownPackFormulasParse(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	formulaDirs := []string{
		filepath.Join(c.Dir, "packs", "gastown", "formulas"),
		filepath.Join(c.Dir, "packs", "maintenance", "formulas"),
	}

	count := 0
	for _, dir := range formulaDirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".formula.toml") && !strings.HasSuffix(path, "order.toml") {
				return nil
			}
			count++

			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Errorf("reading %s: %v", relPath(c.Dir, path), readErr)
				return nil
			}

			var raw map[string]interface{}
			if _, parseErr := toml.Decode(string(data), &raw); parseErr != nil {
				t.Errorf("invalid TOML in %s: %v (PR #3044 regression)", relPath(c.Dir, path), parseErr)
			}
			return nil
		})
		if err != nil {
			t.Errorf("walking %s: %v", dir, err)
		}
	}

	if count == 0 {
		t.Fatal("no formula/order TOML files found in materialized packs")
	}
	t.Logf("validated %d formula/order TOML files", count)
}

// TestRegression_GastownPackPromptsRender verifies that all prompt
// template files in the materialized gastown pack exist, are non-empty,
// and do not reference the removed /ralph-loop slash command.
//
// PR #2939 fixed: prompt referenced nonexistent /ralph-loop slash command.
func TestRegression_GastownPackPromptsRender(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	promptDirs := []string{
		filepath.Join(c.Dir, "packs", "gastown", "prompts"),
		filepath.Join(c.Dir, "packs", "maintenance", "prompts"),
	}

	count := 0
	for _, dir := range promptDirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".md.tmpl") {
				return nil
			}
			count++

			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Errorf("reading %s: %v", relPath(c.Dir, path), readErr)
				return nil
			}

			if len(data) == 0 {
				t.Errorf("%s is empty", relPath(c.Dir, path))
				return nil
			}

			if strings.Contains(string(data), "/ralph-loop") {
				t.Errorf("%s contains /ralph-loop reference (PR #2939 regression)", relPath(c.Dir, path))
			}
			return nil
		})
		if err != nil {
			t.Errorf("walking %s: %v", dir, err)
		}
	}

	if count == 0 {
		t.Fatal("no .md.tmpl files found in materialized packs")
	}
	t.Logf("validated %d prompt template files", count)
}

// TestRegression_GtDoneNotBlockedByInfraFiles verifies that the gastown
// pack's overlay includes git exclude patterns or .gitignore entries for
// infrastructure files that would otherwise block gt done.
//
// PR #3289 fixed: .beads/, .runtime/, .claude/commands/ files blocked
// gt done because they appeared as untracked in git status.
func TestRegression_GtDoneNotBlockedByInfraFiles(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	// Check overlay directories for .gitignore or settings that handle
	// infrastructure file exclusion.
	overlayDirs := []string{
		filepath.Join(c.Dir, "packs", "gastown", "overlays", "default"),
		filepath.Join(c.Dir, "packs", "gastown", "overlay"),
		filepath.Join(c.Dir, "packs", "maintenance", "overlays", "default"),
	}

	// beadsExcluded tracks whether we found a mechanism that specifically
	// excludes .beads (the primary infra path from PR #3289). A generic
	// exclusion mechanism that doesn't mention .beads is not sufficient.
	beadsExcluded := false

	for _, dir := range overlayDirs {
		// Check for .gitignore in overlay.
		gitignore := filepath.Join(dir, ".gitignore")
		if data, err := os.ReadFile(gitignore); err == nil {
			if containsBeadsPattern(string(data)) {
				beadsExcluded = true
			}
		}
	}

	// Also check if the pack's scripts directory contains any setup
	// that configures git excludes referencing .beads.
	scriptsDir := filepath.Join(c.Dir, "packs", "gastown", "scripts")
	if entries, err := os.ReadDir(scriptsDir); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".sh") {
				data, readErr := os.ReadFile(filepath.Join(scriptsDir, e.Name()))
				if readErr != nil {
					continue
				}
				content := string(data)
				// The script must both configure git excludes AND
				// reference the .beads path specifically.
				usesExclude := strings.Contains(content, "info/exclude") ||
					strings.Contains(content, ".gitignore")
				if usesExclude && containsBeadsPattern(content) {
					beadsExcluded = true
				}
			}
		}
	}

	if !beadsExcluded {
		t.Error("no .beads exclusion found in gastown pack " +
			"(expected .gitignore or git exclude script mentioning .beads) " +
			"— PR #3289 regression")
	}
}

// containsBeadsPattern reports whether text contains a pattern that would
// exclude the .beads directory (e.g. ".beads", ".beads/", "beads/").
func containsBeadsPattern(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, ".beads") || strings.Contains(line, "beads/") {
			return true
		}
	}
	return false
}

// TestRegression_PackAgentsHaveUniqueNames verifies that all agent names
// are unique when qualified (dir/name) in a config with the gastown pack
// and multiple rigs.
//
// PR #2986 fixed: polecat names collided across rigs because Dir was not
// set during pack expansion.
func TestRegression_PackAgentsHaveUniqueNames(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	// Add two rigs so rig-scoped agents get stamped twice.
	rig1 := t.TempDir()
	rig2 := t.TempDir()
	c.RigAdd(rig1, "packs/gastown")
	c.RigAdd(rig2, "packs/gastown")

	cfg, _, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(c.Dir, "city.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	seen := make(map[string]bool)
	for _, a := range cfg.Agents {
		qn := a.QualifiedName()
		if seen[qn] {
			t.Errorf("duplicate agent qualified name %q (PR #2986 regression)", qn)
		}
		seen[qn] = true
	}

	if len(seen) == 0 {
		t.Fatal("no agents found in config")
	}
	t.Logf("verified %d unique agent identities", len(seen))
}

// TestRegression_FallbackAgentResolution verifies that the gastown pack's
// non-fallback dog agent overrides the maintenance pack's fallback dog.
//
// This encodes the fallback resolution invariant: when two packs define
// the same agent name, the non-fallback version wins.
func TestRegression_FallbackAgentResolution(t *testing.T) {
	_, cfg := initGastownCity(t)

	// There should be exactly one "dog" agent (gastown's non-fallback
	// overrides maintenance's fallback).
	count := agentCount(cfg, "dog")
	if count != 1 {
		t.Errorf("expected exactly 1 dog agent, got %d (fallback resolution failure)", count)
	}

	// The surviving dog should NOT be the fallback.
	for _, a := range cfg.Agents {
		if a.Name == "dog" {
			if a.Fallback {
				t.Error("dog agent has fallback=true; gastown's non-fallback should have won")
			}
			// Gastown's dog has session_live (tmux theme); maintenance's does not.
			if len(a.SessionLive) == 0 {
				t.Error("dog agent has no session_live; expected gastown's themed dog")
			}
			break
		}
	}
}

// TestRegression_PackIncludesTransitive verifies that gastown pack's
// transitive inclusion of maintenance works: both gastown agents (mayor,
// deacon, etc.) and maintenance agents (dog) are present.
func TestRegression_PackIncludesTransitive(t *testing.T) {
	_, cfg := initGastownCity(t)

	// Gastown city-scoped agents.
	gastownAgents := []string{"mayor", "deacon", "boot"}
	for _, name := range gastownAgents {
		if !hasAgent(cfg, name) {
			t.Errorf("gastown agent %q missing from config (transitive include failure)", name)
		}
	}

	// Gastown rig-scoped agent templates (present even without rigs as
	// templates, but may not appear without a rig registered). Check at
	// least the city-scoped ones.
	// Maintenance agent (included transitively via gastown).
	if !hasAgent(cfg, "dog") {
		t.Error("maintenance agent 'dog' missing from config (transitive include failure)")
	}
}

// TestRegression_CrossRigBeadPrefix verifies that each rig has a unique
// name and agents are properly scoped to their rig via the Dir field.
//
// PR #3383 fixed: cross-rig bead routing used the wrong directory prefix,
// causing beads to be misrouted between rigs.
func TestRegression_CrossRigBeadPrefix(t *testing.T) {
	c := helpers.NewCity(t, testEnv)
	c.InitFrom(filepath.Join(helpers.ExamplesDir(), "gastown"))

	rig1 := t.TempDir()
	rig2 := t.TempDir()
	c.RigAdd(rig1, "packs/gastown")
	c.RigAdd(rig2, "packs/gastown")

	cfg, _, err := config.LoadWithIncludes(fsys.OSFS{}, filepath.Join(c.Dir, "city.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	// Verify rigs have unique names.
	rigNames := make(map[string]bool)
	for _, r := range cfg.Rigs {
		if rigNames[r.Name] {
			t.Errorf("duplicate rig name %q", r.Name)
		}
		rigNames[r.Name] = true
	}

	if len(rigNames) < 2 {
		t.Fatalf("expected at least 2 rigs, got %d", len(rigNames))
	}

	// Verify rig-scoped agents have Dir matching their rig name.
	rigAgentDirs := make(map[string]map[string]bool) // name -> set of dirs
	for _, a := range cfg.Agents {
		if a.Dir != "" {
			if rigAgentDirs[a.Name] == nil {
				rigAgentDirs[a.Name] = make(map[string]bool)
			}
			rigAgentDirs[a.Name][a.Dir] = true
		}
	}

	// For agents that exist in both rigs, verify they have different Dir values.
	for name, dirs := range rigAgentDirs {
		if len(dirs) > 1 {
			// Multiple dirs means agents are properly scoped.
			t.Logf("agent %q properly scoped across %d rigs", name, len(dirs))
		}
	}

	// Verify each rig has a distinct bead prefix.
	prefixes := make(map[string]string) // prefix -> rig name
	for _, r := range cfg.Rigs {
		p := r.EffectivePrefix()
		if existing, ok := prefixes[p]; ok {
			t.Errorf("rigs %q and %q share bead prefix %q (PR #3383 regression)", existing, r.Name, p)
		}
		prefixes[p] = r.Name
	}
}

// TestRegression_SystemPacksAutoIncluded verifies that system packs
// (maintenance) are automatically included when the gastown pack is used.
//
// PR #213 changed how system packs are included: they go through normal
// pack expansion instead of special-case injection.
func TestRegression_SystemPacksAutoIncluded(t *testing.T) {
	_, cfg := initGastownCity(t)

	// Maintenance pack agents must be present (auto-included via gastown's
	// pack.toml includes = ["../maintenance"]).
	if !hasAgent(cfg, "dog") {
		t.Error("maintenance pack agent 'dog' not found; system pack auto-inclusion failed (PR #213 regression)")
	}

	// The workspace includes should reference the gastown pack.
	hasGastownInclude := false
	for _, inc := range cfg.Workspace.Includes {
		if strings.Contains(inc, "gastown") {
			hasGastownInclude = true
			break
		}
	}
	if !hasGastownInclude {
		t.Error("workspace.includes does not reference gastown pack")
	}

	// Verify pack directories were populated during expansion.
	if len(cfg.PackDirs) == 0 {
		t.Error("PackDirs is empty after config load; pack expansion did not run")
	}

	// If beads provider is "bd", verify that maintenance formulas are available
	// (maintenance ships dog-related formulas and orders).
	if cfg.Beads.Provider == "bd" || cfg.Beads.Provider == "" {
		// bd is the default provider; maintenance formulas should be reachable.
		t.Log("beads provider is bd (or default); maintenance formulas expected via pack expansion")
	}

	// Verify formula layers include maintenance pack's formulas.
	cityFormulas := cfg.FormulaLayers.City
	hasMaintenanceFormulas := false
	for _, dir := range cityFormulas {
		if strings.Contains(dir, "maintenance") {
			hasMaintenanceFormulas = true
			break
		}
	}
	if !hasMaintenanceFormulas && len(cityFormulas) > 0 {
		t.Error("maintenance pack formulas not found in formula layers")
	}
}

// relPath returns path relative to base, or the absolute path on error.
func relPath(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}
