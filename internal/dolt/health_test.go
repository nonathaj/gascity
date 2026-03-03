package dolt

import (
	"testing"
)

func TestParseCSVInt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"normal", "COUNT(*)\n42\n", 42},
		{"whitespace", "COUNT(*)\n  42  \n", 42},
		{"zero", "COUNT(*)\n0\n", 0},
		{"empty", "", 0},
		{"header only", "COUNT(*)\n", 0},
		{"non-numeric", "COUNT(*)\nabc\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCSVInt([]byte(tt.in))
			if got != tt.want {
				t.Errorf("parseCSVInt(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestRunHealthCheck_NoServer(t *testing.T) {
	// Run against a temp dir with no Dolt server — should not panic.
	dir := t.TempDir()
	report := RunHealthCheck(dir)

	if report.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
	if report.Server.Running {
		t.Error("Server.Running should be false in temp dir")
	}
	if len(report.Databases) != 0 {
		t.Errorf("Databases should be empty when server not running, got %d", len(report.Databases))
	}
}
