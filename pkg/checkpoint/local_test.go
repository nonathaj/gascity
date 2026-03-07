package checkpoint_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gastownhall/gascity/pkg/checkpoint"
	"github.com/gastownhall/gascity/pkg/checkpoint/checkpointtest"
)

func TestLocalStoreConformance(t *testing.T) {
	checkpointtest.RunStoreTests(t, func() checkpoint.Store {
		return checkpoint.NewLocalStore(t.TempDir())
	})
}

func TestLocalStoreBrokenSymlink(t *testing.T) {
	dir := t.TempDir()
	s := checkpoint.NewLocalStore(dir)
	ctx := context.Background()

	// Save a valid manifest to create the workspace directory.
	m := checkpoint.RecoveryManifest{
		ManifestVersion: 1, WorkspaceID: "ws-1", Epoch: 1,
		SnapshotID: "snap-1", CreatedAt: time.Now().Truncate(time.Second),
	}
	if err := s.Save(ctx, m); err != nil {
		t.Fatal(err)
	}

	// Remove the epoch file but leave the symlink pointing to it.
	epochFile := filepath.Join(dir, "ws-1", "1.json")
	if err := os.Remove(epochFile); err != nil {
		t.Fatal(err)
	}

	// Load should return an error, not silently fall back.
	_, err := s.Load(ctx, "ws-1")
	if err == nil {
		t.Fatal("Load with broken symlink target should return error")
	}
}

func TestLocalStoreSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	s := checkpoint.NewLocalStore(dir)
	ctx := context.Background()

	// Save a valid manifest so the workspace directory exists.
	m := checkpoint.RecoveryManifest{
		ManifestVersion: 1, WorkspaceID: "ws-1", Epoch: 1,
		SnapshotID: "snap-1", CreatedAt: time.Now().Truncate(time.Second),
	}
	if err := s.Save(ctx, m); err != nil {
		t.Fatal(err)
	}

	// Replace the latest symlink with one pointing outside the workspace dir.
	latestPath := filepath.Join(dir, "ws-1", "latest")
	_ = os.Remove(latestPath)
	if err := os.Symlink("../../../etc/passwd", latestPath); err != nil {
		t.Fatal(err)
	}

	// Load should reject the symlink target.
	_, err := s.Load(ctx, "ws-1")
	if err == nil {
		t.Fatal("Load with symlink escape should return error")
	}
}
