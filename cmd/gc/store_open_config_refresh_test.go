package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gastownhall/gascity/internal/config"
)

// TestOpenStoreSkipsBuiltinPackRefreshWhenConfigResolves pins the fast path in
// openStoreResultAtForCityWithAuthority: opening a store reads the city's beads
// settings, so it must NOT re-materialize the generated builtin packs.
//
// That refresh (ensureBuiltinPacksForConfigLoad -> EnsureBuiltinRuntimeAssets)
// measures 1.85s on a city it has not seen and 291ms warm, against ~1ms for the
// actual config parse, and it is keyed per city path — so every fresh city paid it.
// Store opens are on the path of essentially every gc command, and the cost landed
// inside `gc stop`'s pre-flight, which --timeout deliberately does not bound
// (gw-tz9, gw-5us).
//
// The assertion is indirect but exact: EnsureBuiltinRuntimeAssets always
// LoadOrStores an entry for the city under builtinRuntimeReadyCache, so the absence
// of an entry after a store open proves the refresh was never invoked. A city whose
// config genuinely cannot resolve still falls back to the refreshing load — that
// path is deliberately not exercised here, since this test's contract is only that
// a RESOLVABLE config skips the work.
func TestOpenStoreSkipsBuiltinPackRefreshWhenConfigResolves(t *testing.T) {
	cityPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.City{
		Workspace: config.Workspace{Name: "refresh-skip-city"},
		Beads:     config.BeadsConfig{Provider: "file"},
	}
	data, err := cfg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	key := normalizePathForCompare(cityPath)
	if _, seen := builtinRuntimeReadyCache.Load(key); seen {
		t.Fatalf("builtin-pack readiness already recorded for %q before the store open", cityPath)
	}

	// Result intentionally ignored: whether this city yields a usable store is not
	// the contract under test, only that opening it did not trigger the refresh.
	// Nothing to close either — beads.Store.Close takes a bead id; the backing file
	// store lives under t.TempDir() and goes away with it.
	_, _ = openStoreAtForCity(cityPath, cityPath)

	if _, seen := builtinRuntimeReadyCache.Load(key); seen {
		t.Fatalf("opening a store re-materialized builtin packs for %q; "+
			"openStoreResultAtForCityWithAuthority must use the no-refresh config load "+
			"when the config resolves (gw-tz9)", cityPath)
	}
}
