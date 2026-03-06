package sessionlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractTailMetaBasic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Build a minimal JSONL with an assistant message containing model + usage.
	lines := []map[string]any{
		{
			"type":    "user",
			"message": map[string]any{"role": "user", "content": "hello"},
		},
		{
			"type": "assistant",
			"message": map[string]any{
				"role":  "assistant",
				"model": "claude-opus-4-5-20251101",
				"usage": map[string]any{
					"input_tokens":                10000,
					"cache_read_input_tokens":     5000,
					"cache_creation_input_tokens": 2000,
				},
			},
		},
	}

	writeTailJSONL(t, path, lines)

	meta, err := ExtractTailMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("expected non-nil TailMeta")
	}
	if meta.Model != "claude-opus-4-5-20251101" {
		t.Errorf("Model = %q, want %q", meta.Model, "claude-opus-4-5-20251101")
	}
	if meta.ContextUsage == nil {
		t.Fatal("expected non-nil ContextUsage")
	}
	if meta.ContextUsage.InputTokens != 17000 {
		t.Errorf("InputTokens = %d, want 17000", meta.ContextUsage.InputTokens)
	}
	if meta.ContextUsage.ContextWindow != 200_000 {
		t.Errorf("ContextWindow = %d, want 200000", meta.ContextUsage.ContextWindow)
	}
	// 17000/200000 * 100 = 8.5 → int truncation = 8
	if meta.ContextUsage.Percentage != 8 {
		t.Errorf("Percentage = %d, want 8", meta.ContextUsage.Percentage)
	}
}

func TestExtractTailMetaWithCompaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Compaction boundaries are in the file but should NOT affect context
	// usage — the post-compaction assistant usage already reflects the
	// current context window content.
	lines := []map[string]any{
		{
			"type":    "system",
			"subtype": "compact_boundary",
			"compactMetadata": map[string]any{
				"trigger":   "auto",
				"preTokens": 50000,
			},
		},
		{
			"type": "assistant",
			"message": map[string]any{
				"role":  "assistant",
				"model": "claude-sonnet-4-5-20251101",
				"usage": map[string]any{
					"input_tokens":                30000,
					"cache_read_input_tokens":     10000,
					"cache_creation_input_tokens": 0,
				},
			},
		},
	}

	writeTailJSONL(t, path, lines)

	meta, err := ExtractTailMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("expected non-nil TailMeta")
	}
	// 30000 + 10000 + 0 = 40000 (compaction preTokens NOT added)
	if meta.ContextUsage.InputTokens != 40000 {
		t.Errorf("InputTokens = %d, want 40000", meta.ContextUsage.InputTokens)
	}
	// 40000/200000 * 100 = 20
	if meta.ContextUsage.Percentage != 20 {
		t.Errorf("Percentage = %d, want 20", meta.ContextUsage.Percentage)
	}
}

func TestExtractTailMetaEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	meta, err := ExtractTailMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta != nil {
		t.Error("expected nil TailMeta for empty file")
	}
}

func TestExtractTailMetaNoUsage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	lines := []map[string]any{
		{
			"type": "assistant",
			"message": map[string]any{
				"role":  "assistant",
				"model": "claude-opus-4-5-20251101",
			},
		},
	}

	writeTailJSONL(t, path, lines)

	meta, err := ExtractTailMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("expected non-nil TailMeta")
	}
	if meta.Model != "claude-opus-4-5-20251101" {
		t.Errorf("Model = %q, want expected model", meta.Model)
	}
	if meta.ContextUsage != nil {
		t.Error("expected nil ContextUsage when no usage data")
	}
}

func TestExtractTailMetaUnknownModel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	lines := []map[string]any{
		{
			"type": "assistant",
			"message": map[string]any{
				"role":  "assistant",
				"model": "unknown-model-xyz",
				"usage": map[string]any{
					"input_tokens": 10000,
				},
			},
		},
	}

	writeTailJSONL(t, path, lines)

	meta, err := ExtractTailMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta == nil {
		t.Fatal("expected non-nil TailMeta")
	}
	if meta.Model != "unknown-model-xyz" {
		t.Errorf("Model = %q, want %q", meta.Model, "unknown-model-xyz")
	}
	// Unknown model → no context window → no usage
	if meta.ContextUsage != nil {
		t.Error("expected nil ContextUsage for unknown model")
	}
}

func TestExtractTailMetaMissingFile(t *testing.T) {
	_, err := ExtractTailMeta("/nonexistent/path.jsonl")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func writeTailJSONL(t *testing.T, path string, lines []map[string]any) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck // test helper
	enc := json.NewEncoder(f)
	for _, line := range lines {
		if err := enc.Encode(line); err != nil {
			t.Fatal(err)
		}
	}
}
