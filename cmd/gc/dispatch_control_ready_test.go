package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
)

func TestParseControlReadyQueryRecognizesGeneratedQuery(t *testing.T) {
	// Dir+Name shaped so QualifiedName is a rig-scoped, binding-qualified
	// name ("fixture/core.control-dispatcher"): this is the only shape that
	// produces a non-empty bare-route alias (see TestControlDispatcherBareRoute).
	query := workflowServeControlReadyQuery(config.Agent{Name: "core.control-dispatcher", Dir: "fixture"}, "gascity--control-dispatcher")
	parsed, ok := parseControlReadyQuery(query)
	if !ok {
		t.Fatalf("parseControlReadyQuery: not recognized: %q", query)
	}
	if parsed.target != "fixture/core.control-dispatcher" {
		t.Errorf("target = %q, want %q", parsed.target, "fixture/core.control-dispatcher")
	}
	if parsed.controlSessionName != "gascity--control-dispatcher" {
		t.Errorf("controlSessionName = %q, want %q", parsed.controlSessionName, "gascity--control-dispatcher")
	}
	if parsed.bareTarget != "fixture/control-dispatcher" {
		t.Errorf("bareTarget = %q, want %q", parsed.bareTarget, "fixture/control-dispatcher")
	}
	if parsed.includeEphemeral {
		t.Errorf("includeEphemeral = true, want false (bd-1.0.4 default)")
	}
}

func TestParseControlReadyQueryIncludeEphemeralWhenBD105(t *testing.T) {
	query := workflowServeControlReadyQueryForBeads(
		config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"},
		config.BeadsConfig{BDCompatibility: config.BeadsBDCompatibility105},
	)
	parsed, ok := parseControlReadyQuery(query)
	if !ok {
		t.Fatalf("parseControlReadyQuery: not recognized")
	}
	if !parsed.includeEphemeral {
		t.Errorf("includeEphemeral = false, want true under bd-1.0.5 compatibility")
	}
}

func TestParseControlReadyQueryRejectsNonControlQuery(t *testing.T) {
	for _, q := range []string{
		"",
		"bd ready --json --limit=20",
		"GC_CONTROL_TARGET=core.control-dispatcher sh -c 'bd ready'", // missing the BD_EXPORT_AUTO=false marker prefix
	} {
		if _, ok := parseControlReadyQuery(q); ok {
			t.Errorf("parseControlReadyQuery(%q) = ok, want not recognized", q)
		}
	}
}

func TestControlReadyCandidatesPrecedenceDedupAndLegacyExpansion(t *testing.T) {
	query := workflowServeControlReadyQuery(config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"})
	parsed, ok := parseControlReadyQuery(query)
	if !ok {
		t.Fatalf("parseControlReadyQuery: not recognized")
	}
	envList := []string{
		"GC_SESSION_NAME=gascity--control-dispatcher",
		"GC_ALIAS=gascity/control-dispatcher",
	}

	got := controlReadyCandidates(parsed, envList)
	want := []string{
		"gascity--control-dispatcher",
		"gascity--workflow-control",
		"gascity/control-dispatcher",
		"gascity/workflow-control",
	}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("controlReadyCandidates = %#v, want %#v", got, want)
	}
}

func TestControlReadyCandidatesSkipsEmptySlots(t *testing.T) {
	// "control-dispatcher" itself ends in the literal suffix "control-dispatcher",
	// so it also produces the bare "workflow-control" legacy variant.
	parsed := parsedControlReadyQuery{target: "control-dispatcher"}
	got := controlReadyCandidates(parsed, nil)
	want := []string{"control-dispatcher", "workflow-control"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("controlReadyCandidates = %#v, want %#v", got, want)
	}
}

func TestControlReadyRoutesFiltersEmptyAliases(t *testing.T) {
	parsed := parsedControlReadyQuery{target: "core.control-dispatcher", bareTarget: "control-dispatcher"}
	got := controlReadyRoutes(parsed)
	want := []string{"core.control-dispatcher", "control-dispatcher"}
	if !stringSlicesEqual(got, want) {
		t.Fatalf("controlReadyRoutes = %#v, want %#v", got, want)
	}
}

func TestFilterReadyByAssigneeExcludesEpicAndOtherAssignees(t *testing.T) {
	ready := []beads.Bead{
		{ID: "ga-epic-leak", Assignee: "cand", Type: "epic"},
		{ID: "ga-ready", Assignee: "cand", Type: "task"},
		{ID: "ga-other", Assignee: "someone-else", Type: "task"},
	}
	got := filterReadyByAssignee(ready, "cand", workflowServeScanLimit)
	if len(got) != 1 || got[0].ID != "ga-ready" {
		t.Fatalf("filterReadyByAssignee = %#v, want only ga-ready", got)
	}
}

func TestFilterReadyByAssigneeRespectsLimit(t *testing.T) {
	ready := make([]beads.Bead, 0, 5)
	for i := 0; i < 5; i++ {
		ready = append(ready, beads.Bead{ID: strings.Repeat("z", i+1), Assignee: "cand", Type: "task"})
	}
	got := filterReadyByAssignee(ready, "cand", 2)
	if len(got) != 2 {
		t.Fatalf("filterReadyByAssignee len = %d, want 2", len(got))
	}
}

func TestFilterReadyByRouteRequiresUnassignedAndSortsOldestFirst(t *testing.T) {
	newer := time.Unix(200, 0)
	older := time.Unix(100, 0)
	ready := []beads.Bead{
		{ID: "ga-assigned-routed", CreatedAt: older, Assignee: "someone", Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "core/control-dispatcher"}},
		{ID: "ga-newer", CreatedAt: newer, Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "core/control-dispatcher"}},
		{ID: "ga-older", CreatedAt: older, Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "core/control-dispatcher"}},
		{ID: "ga-epic-routed", CreatedAt: older, Type: "epic", Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "core/control-dispatcher"}},
		{ID: "ga-other-route", CreatedAt: older, Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "other"}},
	}
	got := filterReadyByRoute(ready, beadmeta.RunTargetMetadataKey, "core/control-dispatcher")
	want := []string{"ga-older", "ga-newer"}
	if !stringSlicesEqual(beadIDs(got), want) {
		t.Fatalf("filterReadyByRoute = %#v, want %#v", beadIDs(got), want)
	}
}

func TestMergeControlReadyGroupsDedupsPreservingFirstOccurrence(t *testing.T) {
	assigned := []beads.Bead{
		{ID: "ga-z-assigned"},
		{ID: "ga-dup", Metadata: map[string]string{"source": "assigned"}},
	}
	runTargetRouted := []beads.Bead{
		{ID: "ga-a-routed"},
		{ID: "ga-route-dup", Metadata: map[string]string{"source": "run-target"}},
	}
	routedToRouted := []beads.Bead{
		{ID: "ga-route-dup", Metadata: map[string]string{"source": "routed-to"}},
	}

	got := mergeControlReadyGroups(assigned, runTargetRouted, routedToRouted)
	wantIDs := []string{"ga-z-assigned", "ga-dup", "ga-a-routed", "ga-route-dup"}
	if !stringSlicesEqual(beadIDs(got), wantIDs) {
		t.Fatalf("mergeControlReadyGroups ids = %#v, want %#v", beadIDs(got), wantIDs)
	}
	for _, b := range got {
		if b.ID == "ga-route-dup" && b.Metadata["source"] != "run-target" {
			t.Fatalf("ga-route-dup source = %q, want first-seen %q", b.Metadata["source"], "run-target")
		}
	}
}

func TestMergeControlReadyGroupsSkipsInstantiatingWithoutMarkingSeen(t *testing.T) {
	assigned := []beads.Bead{
		{ID: "ga-instantiating-assigned", Metadata: map[string]string{beadmeta.InstantiatingMetadataKey: "true"}},
		{ID: "ga-assigned", Metadata: map[string]string{"gc.kind": "retry"}},
	}
	runTargetRouted := []beads.Bead{
		{ID: "ga-instantiating-routed", Metadata: map[string]string{beadmeta.InstantiatingMetadataKey: "true"}},
		{ID: "ga-routed", Metadata: map[string]string{"gc.kind": "scope-check"}},
	}
	// A later group re-surfacing the SAME id without the instantiating tag
	// must still be admitted -- the shell's jq reduce never marks an
	// instantiating occurrence as "seen".
	laterNonInstantiating := []beads.Bead{
		{ID: "ga-instantiating-assigned", Metadata: map[string]string{"gc.kind": "now-real"}},
	}

	got := mergeControlReadyGroups(assigned, runTargetRouted, laterNonInstantiating)
	wantIDs := []string{"ga-assigned", "ga-routed", "ga-instantiating-assigned"}
	if !stringSlicesEqual(beadIDs(got), wantIDs) {
		t.Fatalf("mergeControlReadyGroups ids = %#v, want %#v", beadIDs(got), wantIDs)
	}
}

// TestEvaluateControlReadyMatchesShellQueryPriority ports
// TestWorkflowServeControlReadyQueryPreservesQueryPriorityWhenMerging's
// scenario (cmd_convoy_dispatch_test.go) at the Go level: given the same
// parsed query + env, and a ready set shaped like what CachedReady/the
// batched fallback would return, evaluateControlReady must merge candidates
// before routes and drop later ID duplicates exactly like the shell's jq
// reduce does.
func TestEvaluateControlReadyMatchesShellQueryPriority(t *testing.T) {
	query := workflowServeControlReadyQuery(config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"})
	parsed, ok := parseControlReadyQuery(query)
	if !ok {
		t.Fatalf("parseControlReadyQuery: not recognized")
	}
	envList := []string{
		"GC_SESSION_NAME=gascity--control-dispatcher",
		"GC_ALIAS=gascity/control-dispatcher",
	}
	ready := []beads.Bead{
		{ID: "ga-z-assigned", Assignee: "gascity--control-dispatcher"},
		{ID: "ga-dup", Assignee: "gascity--control-dispatcher", Metadata: map[string]string{"source": "assigned"}},
		{ID: "ga-a-routed", Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "gascity/control-dispatcher"}},
		{ID: "ga-route-dup", Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "gascity/control-dispatcher", "source": "run-target"}},
		{ID: "ga-route-dup-2", Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "gascity/control-dispatcher"}},
	}
	// ga-route-dup also appears as a routed_to match with different content;
	// the run_target occurrence (checked first) must win.
	ready = append(ready, beads.Bead{ID: "ga-route-dup", Metadata: map[string]string{beadmeta.RoutedToMetadataKey: "gascity/control-dispatcher", "source": "routed-to"}})

	got := evaluateControlReady(ready, parsed, envList)
	wantIDs := []string{"ga-z-assigned", "ga-dup", "ga-a-routed", "ga-route-dup", "ga-route-dup-2"}
	if !stringSlicesEqual(beadIDs(got), wantIDs) {
		t.Fatalf("evaluateControlReady ids = %#v, want %#v", beadIDs(got), wantIDs)
	}
	for _, b := range got {
		if b.ID == "ga-route-dup" && b.Metadata["source"] != "run-target" {
			t.Fatalf("ga-route-dup source = %q, want first-seen %q", b.Metadata["source"], "run-target")
		}
	}
}

func TestEvaluateControlReadyExcludesEpicAndInstantiating(t *testing.T) {
	query := workflowServeControlReadyQuery(config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"})
	parsed, ok := parseControlReadyQuery(query)
	if !ok {
		t.Fatalf("parseControlReadyQuery: not recognized")
	}
	envList := []string{
		"GC_SESSION_NAME=gascity--control-dispatcher",
		"GC_ALIAS=gascity/control-dispatcher",
	}
	ready := []beads.Bead{
		{ID: "ga-epic-leak", Assignee: "gascity--control-dispatcher", Type: "epic"},
		{ID: "ga-ready", Assignee: "gascity--control-dispatcher", Type: "task"},
		{ID: "ga-instantiating-routed", Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "gascity/control-dispatcher", beadmeta.InstantiatingMetadataKey: "true"}},
		{ID: "ga-routed", Metadata: map[string]string{beadmeta.RunTargetMetadataKey: "gascity/control-dispatcher", "gc.kind": "scope-check"}},
	}

	got := evaluateControlReady(ready, parsed, envList)
	wantIDs := []string{"ga-ready", "ga-routed"}
	if !stringSlicesEqual(beadIDs(got), wantIDs) {
		t.Fatalf("evaluateControlReady ids = %#v, want %#v", beadIDs(got), wantIDs)
	}
}

func beadIDs(items []beads.Bead) []string {
	out := make([]string, len(items))
	for i, b := range items {
		out[i] = b.ID
	}
	return out
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- End-to-end: nextWorkflowServeBeads wiring (cache + fallback) ---

// setUpControlReadyFileStoreCity builds a scope-local FileStore-backed city
// so tryControlReadyFromCacheOrFallback's cache path can PrimeActive() and
// CachedReady() without any bd/dolt process at all, and returns the opened
// store for seeding fixture beads directly.
func setUpControlReadyFileStoreCity(t *testing.T) (cityDir string, store *beads.FileStore) {
	t.Helper()
	configureIsolatedRuntimeEnv(t)
	t.Setenv("GC_BEADS", "file")

	cityDir = t.TempDir()
	if err := ensureScopedFileStoreLayout(cityDir); err != nil {
		t.Fatalf("ensureScopedFileStoreLayout: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte("[workspace]\nname = \"test-city\"\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}
	if err := ensurePersistedScopeLocalFileStore(cityDir); err != nil {
		t.Fatalf("ensurePersistedScopeLocalFileStore: %v", err)
	}
	store, err := openScopeLocalFileStore(cityDir)
	if err != nil {
		t.Fatalf("openScopeLocalFileStore: %v", err)
	}
	return cityDir, store
}

// noBDOnPathForTest ensures no bd (or bd stub) is reachable via PATH, so a
// test can prove a code path made zero subprocess calls: any shell-out would
// fail with "command not found" rather than silently succeeding.
func noBDOnPathForTest(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestTryControlReadyFromCacheOrFallbackAnswersFromCacheWithZeroSubprocessCalls(t *testing.T) {
	cityDir, store := setUpControlReadyFileStoreCity(t)
	noBDOnPathForTest(t)

	target := "gascity/control-dispatcher"
	ready, err := store.Create(beads.Bead{Assignee: target, Type: "task"})
	if err != nil {
		t.Fatalf("create ready bead: %v", err)
	}
	epic, err := store.Create(beads.Bead{Assignee: target, Type: "epic"})
	if err != nil {
		t.Fatalf("create epic bead: %v", err)
	}
	routed, err := store.Create(beads.Bead{Metadata: map[string]string{beadmeta.RoutedToMetadataKey: target}})
	if err != nil {
		t.Fatalf("create routed bead: %v", err)
	}

	agentCfg := config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"}
	query := workflowServeControlReadyQuery(agentCfg)

	queue, handled, err := tryControlReadyFromCacheOrFallback(query, cityDir, nil)
	if err != nil {
		t.Fatalf("tryControlReadyFromCacheOrFallback: %v", err)
	}
	if !handled {
		t.Fatalf("tryControlReadyFromCacheOrFallback: handled = false, want true for a control-ready query")
	}

	var gotIDs []string
	for _, b := range queue {
		gotIDs = append(gotIDs, b.ID)
	}
	wantIDs := []string{ready.ID, routed.ID}
	if !stringSlicesEqual(gotIDs, wantIDs) {
		t.Fatalf("queue ids = %#v, want %#v (epic bead %s must be excluded)", gotIDs, wantIDs, epic.ID)
	}
}

func TestTryControlReadyFromCacheOrFallbackReturnsUnhandledForNonControlQuery(t *testing.T) {
	cityDir := t.TempDir()
	_, handled, err := tryControlReadyFromCacheOrFallback("bd ready --json --limit=20", cityDir, nil)
	if handled {
		t.Fatalf("handled = true, want false for a non-control-ready query")
	}
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

// TestTryControlReadyFromCacheOrFallbackUsesSingleBatchedBDCallWhenCacheUnavailable
// forces the cache path to fail (PrimeActive against a bd stub that errors on
// `list`) and asserts the fallback makes exactly one bd invocation covering
// the whole tick, not the shell script's N per-candidate/route calls.
func TestTryControlReadyFromCacheOrFallbackUsesSingleBatchedBDCallWhenCacheUnavailable(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	cityDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cityDir, "city.toml"), []byte("[workspace]\nname = \"test-city\"\n"), 0o644); err != nil {
		t.Fatalf("write city.toml: %v", err)
	}

	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "bd.log")
	target := "gascity/control-dispatcher"
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> "%s"
case "$1" in
  list)
    exit 7
    ;;
esac
case "$*" in
  "--readonly --sandbox ready --json --exclude-type=epic --limit=%d")
    printf '[{"id":"ga-fallback-ready","assignee":"%s"}]'
    ;;
  *)
    printf '[]'
    ;;
esac
`, logPath, controlReadyFallbackLimit, target)
	installFakeToolOnPath(t, tmp, "bd", script)
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GC_BEADS", "bd")

	agentCfg := config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"}
	query := workflowServeControlReadyQuery(agentCfg)

	queue, handled, err := tryControlReadyFromCacheOrFallback(query, cityDir, nil)
	if err != nil {
		t.Fatalf("tryControlReadyFromCacheOrFallback: %v", err)
	}
	if !handled {
		t.Fatalf("handled = false, want true")
	}
	if len(queue) != 1 || queue[0].ID != "ga-fallback-ready" {
		t.Fatalf("queue = %#v, want single ga-fallback-ready bead", queue)
	}

	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read bd log: %v", err)
	}
	calls := strings.Split(strings.TrimSpace(string(logData)), "\n")
	readyCalls := 0
	for _, c := range calls {
		if strings.HasPrefix(c, "--readonly --sandbox ready") {
			readyCalls++
		}
	}
	if readyCalls != 1 {
		t.Fatalf("bd ready calls = %d, want exactly 1; all calls:\n%s", readyCalls, string(logData))
	}
}

// installHealthyEmptyLedgerBDStub installs a bd that records every invocation
// and answers like a healthy, empty ledger, so a snapshot prime against it
// SUCCEEDS; only the batched ready read returns anything: one control bead
// assigned to target. It returns the invocation log path.
func installHealthyEmptyLedgerBDStub(t *testing.T, target string) string {
	t.Helper()
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "bd.log")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> "%s"
case "$*" in
  "--readonly --sandbox ready --json --exclude-type=epic --limit=%d")
    printf '[{"id":"ga-batched-ready","assignee":"%s"}]'
    ;;
  *)
    printf '[]'
    ;;
esac
`, logPath, controlReadyFallbackLimit, target)
	installFakeToolOnPath(t, tmp, "bd", script)
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GC_BEADS", "bd")
	return logPath
}

// bdStubCalls returns the logged bd invocations whose argv starts with prefix.
func bdStubCalls(t *testing.T, logPath, prefix string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read bd log: %v", err)
	}
	var matched []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line != "" && strings.HasPrefix(line, prefix) {
			matched = append(matched, line)
		}
	}
	return matched
}

// TestTryControlReadyFromCacheOrFallbackBDScopeTakesOneBatchedReadPerScan pins
// the idle cost of a bd-backed control dispatcher (gcty-fz6r). Every read a
// snapshot prime makes against a bd-backed scope is its own bd subprocess — the
// open and in_progress lists for each tier, the ready projection — and the
// snapshot lives for controlReadyCacheTTL, shorter than the serve loop's idle
// sweep, so an idle loop re-paid the whole prime on almost every scan. The one
// batched ready read answers the same scan with a single subprocess, so on a
// bd-backed scope that read is the whole scan, every time, even when a prime
// would have succeeded.
func TestTryControlReadyFromCacheOrFallbackBDScopeTakesOneBatchedReadPerScan(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	resetControlReadyCache(t)
	cityDir := t.TempDir()
	writeMinimalCityToml(t, cityDir)
	target := "gascity/control-dispatcher"
	logPath := installHealthyEmptyLedgerBDStub(t, target)

	query := workflowServeControlReadyQuery(config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"})
	const scans = 3
	for i := 0; i < scans; i++ {
		queue, handled, err := tryControlReadyFromCacheOrFallback(query, cityDir, nil)
		if err != nil {
			t.Fatalf("scan %d: tryControlReadyFromCacheOrFallback: %v", i, err)
		}
		if !handled {
			t.Fatalf("scan %d: handled = false, want true for a control-ready query", i)
		}
		if len(queue) != 1 || queue[0].ID != "ga-batched-ready" {
			t.Fatalf("scan %d: queue = %#v, want the batched read's ga-batched-ready", i, queue)
		}
	}

	if lists := bdStubCalls(t, logPath, "list"); len(lists) != 0 {
		t.Errorf("bd list calls = %d, want 0: a bd-backed scope primed a ready snapshot, one bd subprocess per read, instead of taking the batched read:\n%s",
			len(lists), strings.Join(lists, "\n"))
	}
	if readies := bdStubCalls(t, logPath, "--readonly --sandbox ready"); len(readies) != scans {
		t.Errorf("batched bd ready calls = %d over %d scans, want exactly one per scan", len(readies), scans)
	}
}

// TestTryControlReadyFromCacheOrFallbackBDScopeDoesNotReloadConfigPerScan is
// the other half of that idle cost: the scan loaded the whole city config —
// city.toml and every pack include — on every call, only to hand it to the
// snapshot's store open. A scope whose scan never primes a snapshot must not
// pay for one; at most the first scan may resolve the config to learn that.
func TestTryControlReadyFromCacheOrFallbackBDScopeDoesNotReloadConfigPerScan(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	resetControlReadyCache(t)
	cityDir := t.TempDir()
	writeMinimalCityToml(t, cityDir)
	target := "gascity/control-dispatcher"
	installHealthyEmptyLedgerBDStub(t, target)

	query := workflowServeControlReadyQuery(config.Agent{Name: config.ControlDispatcherAgentName, Dir: "gascity"})
	const scans = 5
	before := cityConfigLoadCount()
	for i := 0; i < scans; i++ {
		if _, _, err := tryControlReadyFromCacheOrFallback(query, cityDir, nil); err != nil {
			t.Fatalf("scan %d: tryControlReadyFromCacheOrFallback: %v", i, err)
		}
	}
	if loads := cityConfigLoadCount() - before; loads > 1 {
		t.Fatalf("city config loads over %d scans = %d, want at most 1: the idle scan re-resolves the whole city config on every call", scans, loads)
	}
}

// TestControlReadyFallbackReadyLogsWhenResultHitsLimit is ga-bbj6wv Finding 1:
// a fallback batch that comes back at exactly controlReadyFallbackLimit is a
// truncation signal (some candidate/route may have been starved of ready
// beads that exist but didn't fit) and must be observable, not silent.
func TestControlReadyFallbackReadyLogsWhenResultHitsLimit(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	tmp := t.TempDir()

	items := make([]map[string]string, controlReadyFallbackLimit)
	for i := range items {
		items[i] = map[string]string{"id": fmt.Sprintf("ga-fallback-%d", i)}
	}
	payload, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal fixture beads: %v", err)
	}
	payloadPath := filepath.Join(tmp, "payload.json")
	if err := os.WriteFile(payloadPath, payload, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	script := fmt.Sprintf("#!/bin/sh\ncat %q\n", payloadPath)
	installFakeToolOnPath(t, tmp, "bd", script)
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GC_BEADS", "bd")

	var logBuf bytes.Buffer
	restore := captureLogOutput(&logBuf)
	defer restore()

	dir := t.TempDir()
	result, err := controlReadyFallbackReady(dir, dir, nil, false)
	if err != nil {
		t.Fatalf("controlReadyFallbackReady: %v", err)
	}
	if len(result) != controlReadyFallbackLimit {
		t.Fatalf("len(result) = %d, want %d", len(result), controlReadyFallbackLimit)
	}
	if !strings.Contains(logBuf.String(), "may be truncated") {
		t.Fatalf("expected a truncation warning in log output, got: %q", logBuf.String())
	}
	if !strings.Contains(logBuf.String(), dir) {
		t.Fatalf("expected log to name the dir %q, got: %q", dir, logBuf.String())
	}
}

// TestControlReadyFallbackReadyNoWarningBelowLimit is the negative case: a
// batch below the limit is a complete result, not a truncation signal, and
// must not log anything.
func TestControlReadyFallbackReadyNoWarningBelowLimit(t *testing.T) {
	configureIsolatedRuntimeEnv(t)
	tmp := t.TempDir()
	installFakeToolOnPath(t, tmp, "bd", "#!/bin/sh\nprintf '[{\"id\":\"ga-fallback-only\"}]'\n")
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GC_BEADS", "bd")

	var logBuf bytes.Buffer
	restore := captureLogOutput(&logBuf)
	defer restore()

	dir := t.TempDir()
	result, err := controlReadyFallbackReady(dir, dir, nil, false)
	if err != nil {
		t.Fatalf("controlReadyFallbackReady: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(result))
	}
	if logBuf.Len() != 0 {
		t.Fatalf("expected no log output below the limit, got: %q", logBuf.String())
	}
}

func TestNextWorkflowServeBeadsNonControlQueryUsesOriginalShellPath(t *testing.T) {
	tmp := t.TempDir()
	installFakeToolOnPath(t, tmp, "bd", `#!/bin/sh
printf '[{"id":"ga-plain"}]'
`)
	t.Setenv("PATH", tmp+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := nextWorkflowServeBeads("bd ready --json --limit=20", t.TempDir(), nil)
	if err != nil {
		t.Fatalf("nextWorkflowServeBeads: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ga-plain" {
		t.Fatalf("nextWorkflowServeBeads = %#v, want [{ga-plain}]", got)
	}
}
