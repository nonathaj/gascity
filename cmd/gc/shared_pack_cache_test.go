package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The shared cache directory is created on first opt-in and reused by every
// later one. Kept as plain state rather than sync.OnceValues so cleanup can
// tell "never created" from "created", instead of creating the directory in
// order to delete it.
var (
	sharedPackCacheOnce sync.Once
	sharedPackCacheDir  string
	sharedPackCacheErr  error
)

func sharedBuiltinPackCacheRoot() (string, error) {
	sharedPackCacheOnce.Do(func() {
		sharedPackCacheDir, sharedPackCacheErr = os.MkdirTemp("", "gc-shared-pack-cache")
	})
	return sharedPackCacheDir, sharedPackCacheErr
}

// useSharedBuiltinPackCache points the bundled-pack repo cache at one
// directory shared by the whole test binary, instead of the per-test GC_HOME.
//
// Tests rotate GC_HOME to isolate city and home state. The bundled pack cache
// underneath it is neither: it is content-addressed by source+commit and
// identical for every test in the binary, so rotating it only means each test
// rebuilds the same 492-file, 220-directory tree and then deletes it again.
// That is ~1.4s of pure overhead per materialization on Windows, where the
// cost is charged per filesystem entry rather than per byte (see
// engdocs/contributors/windows-portability.md), and it was 62% of the runtime
// of the slowest cmd/gc unit test.
//
// This is opt-in rather than automatic in TestMain for one specific reason: a
// test that computes a cache path itself -- filepath.Join(gcHome, ".gc",
// "cache", "repos", ...) or config.GlobalRepoCachePath(gcHome, ...) -- is
// asserting where the cache lives, and would look in the wrong place if the
// override moved it. Call this only from tests that never make that
// assertion. It also cannot be used from a parallel test, because it sets an
// environment variable.
//
// GC_HOME isolation is untouched: only this one cache moves.
func useSharedBuiltinPackCache(t *testing.T) {
	t.Helper()
	root, err := sharedBuiltinPackCacheRoot()
	if err != nil {
		t.Fatalf("create shared pack cache root: %v", err)
	}
	t.Setenv("GC_REPO_CACHE_ROOT", root)
}

// removeSharedBuiltinPackCache deletes the shared cache directory if one was
// created. A run where no test opted in creates nothing and removes nothing.
func removeSharedBuiltinPackCache() {
	if sharedPackCacheDir == "" {
		return
	}
	_ = os.RemoveAll(filepath.Clean(sharedPackCacheDir))
}
