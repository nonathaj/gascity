package bd

import (
	"testing"

	"github.com/gastownhall/gascity/internal/formula"
)

func TestMolDoWorkParses(t *testing.T) {
	data, err := PackFS.ReadFile("formulas/mol-do-work.formula.toml")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	f, err := formula.Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := formula.Validate(f); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if f.Name != "mol-do-work" {
		t.Errorf("Name = %q, want %q", f.Name, "mol-do-work")
	}
	if len(f.Steps) != 1 {
		t.Fatalf("len(Steps) = %d, want 1", len(f.Steps))
	}
	if f.Steps[0].ID != "do-work" {
		t.Errorf("Steps[0].ID = %q, want %q", f.Steps[0].ID, "do-work")
	}
}
