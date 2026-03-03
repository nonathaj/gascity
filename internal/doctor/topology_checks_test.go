package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCheckScript(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "check.sh")
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTopologyScriptCheckOK(t *testing.T) {
	dir := t.TempDir()
	script := writeCheckScript(t, dir, "#!/bin/sh\necho 'all good'\nexit 0\n")

	c := &TopologyScriptCheck{
		CheckName:   "test-topo:check",
		Script:      script,
		TopologyDir: dir,
	}

	if c.Name() != "test-topo:check" {
		t.Errorf("Name() = %q, want %q", c.Name(), "test-topo:check")
	}
	if c.CanFix() {
		t.Error("CanFix() should return false")
	}

	ctx := &CheckContext{CityPath: dir}
	result := c.Run(ctx)

	if result.Status != StatusOK {
		t.Errorf("Status = %d, want StatusOK", result.Status)
	}
	if result.Message != "all good" {
		t.Errorf("Message = %q, want %q", result.Message, "all good")
	}
}

func TestTopologyScriptCheckWarning(t *testing.T) {
	dir := t.TempDir()
	script := writeCheckScript(t, dir, "#!/bin/sh\necho 'minor issue'\necho 'detail one'\nexit 1\n")

	c := &TopologyScriptCheck{
		CheckName:   "topo:warn",
		Script:      script,
		TopologyDir: dir,
	}

	result := c.Run(&CheckContext{CityPath: dir})

	if result.Status != StatusWarning {
		t.Errorf("Status = %d, want StatusWarning", result.Status)
	}
	if result.Message != "minor issue" {
		t.Errorf("Message = %q, want %q", result.Message, "minor issue")
	}
	if len(result.Details) != 1 || result.Details[0] != "detail one" {
		t.Errorf("Details = %v, want [detail one]", result.Details)
	}
}

func TestTopologyScriptCheckError(t *testing.T) {
	dir := t.TempDir()
	script := writeCheckScript(t, dir, "#!/bin/sh\necho 'missing binary'\necho 'foo not found'\necho 'bar not found'\nexit 2\n")

	c := &TopologyScriptCheck{
		CheckName:   "topo:err",
		Script:      script,
		TopologyDir: dir,
	}

	result := c.Run(&CheckContext{CityPath: dir})

	if result.Status != StatusError {
		t.Errorf("Status = %d, want StatusError", result.Status)
	}
	if result.Message != "missing binary" {
		t.Errorf("Message = %q, want %q", result.Message, "missing binary")
	}
	if len(result.Details) != 2 {
		t.Errorf("Details count = %d, want 2", len(result.Details))
	}
}

func TestTopologyScriptCheckNotFound(t *testing.T) {
	c := &TopologyScriptCheck{
		CheckName:   "topo:missing",
		Script:      "/nonexistent/script.sh",
		TopologyDir: t.TempDir(),
	}

	result := c.Run(&CheckContext{CityPath: t.TempDir()})

	if result.Status != StatusError {
		t.Errorf("Status = %d, want StatusError", result.Status)
	}
	if result.Message == "" {
		t.Error("Message should not be empty for missing script")
	}
}

func TestTopologyScriptCheckNotExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "check.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c := &TopologyScriptCheck{
		CheckName:   "topo:noexec",
		Script:      path,
		TopologyDir: dir,
	}

	result := c.Run(&CheckContext{CityPath: dir})

	if result.Status != StatusError {
		t.Errorf("Status = %d, want StatusError", result.Status)
	}
}

func TestTopologyScriptCheckEmptyOutput(t *testing.T) {
	dir := t.TempDir()
	script := writeCheckScript(t, dir, "#!/bin/sh\nexit 0\n")

	c := &TopologyScriptCheck{
		CheckName:   "topo:empty",
		Script:      script,
		TopologyDir: dir,
	}

	result := c.Run(&CheckContext{CityPath: dir})

	if result.Status != StatusOK {
		t.Errorf("Status = %d, want StatusOK", result.Status)
	}
	if result.Message != "check completed" {
		t.Errorf("Message = %q, want %q", result.Message, "check completed")
	}
}

func TestTopologyScriptCheckEnvVars(t *testing.T) {
	dir := t.TempDir()
	cityPath := t.TempDir()
	// Script echoes env vars to verify they're passed.
	script := writeCheckScript(t, dir,
		"#!/bin/sh\necho \"city=$GC_CITY_PATH topo=$GC_TOPOLOGY_DIR\"\nexit 0\n")

	c := &TopologyScriptCheck{
		CheckName:   "topo:env",
		Script:      script,
		TopologyDir: dir,
	}

	result := c.Run(&CheckContext{CityPath: cityPath})

	if result.Status != StatusOK {
		t.Errorf("Status = %d, want StatusOK", result.Status)
	}
	expected := "city=" + cityPath + " topo=" + dir
	if result.Message != expected {
		t.Errorf("Message = %q, want %q", result.Message, expected)
	}
}

func TestParseScriptOutput(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantMsg    string
		wantDetail int
	}{
		{"empty", "", "", 0},
		{"single line", "hello\n", "hello", 0},
		{"message and details", "msg\ndetail1\ndetail2\n", "msg", 2},
		{"blank lines skipped", "msg\n\n  \ndetail\n\n", "msg", 1},
		{"whitespace trimmed", "  msg  \n  detail  \n", "msg", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, details := parseScriptOutput(tt.input)
			if msg != tt.wantMsg {
				t.Errorf("message = %q, want %q", msg, tt.wantMsg)
			}
			if len(details) != tt.wantDetail {
				t.Errorf("details count = %d, want %d", len(details), tt.wantDetail)
			}
		})
	}
}
