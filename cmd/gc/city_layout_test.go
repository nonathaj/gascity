package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacyContentPrunesEmptyLegacyDir(t *testing.T) {
	cityDir := t.TempDir()
	legacyDir := filepath.Join(cityDir, ".gc", "prompts")
	canonicalDir := filepath.Join(cityDir, "prompts")
	legacyFile := filepath.Join(legacyDir, "mayor.md")
	canonicalFile := filepath.Join(canonicalDir, "mayor.md")

	for _, path := range []string{legacyDir, canonicalDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", path, err)
		}
	}
	for _, path := range []string{legacyFile, canonicalFile} {
		if err := os.WriteFile(path, []byte("same"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", path, err)
		}
	}

	if err := migrateLegacyContent(legacyDir, canonicalDir); err != nil {
		t.Fatalf("migrateLegacyContent(%q, %q): %v", legacyDir, canonicalDir, err)
	}

	if _, err := os.Stat(legacyDir); !os.IsNotExist(err) {
		t.Fatalf("legacy directory still exists: %q", legacyDir)
	}
}
