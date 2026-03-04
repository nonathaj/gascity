package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/gascity/internal/agent"
	"github.com/steveyegge/gascity/internal/fsys"
)

// ---------------------------------------------------------------------------
// doAgentAttach tests (no existing coverage)
// ---------------------------------------------------------------------------

func TestDoAgentAttachStartsThenAttaches(t *testing.T) {
	f := agent.NewFake("mayor", "mayor")

	var stdout, stderr bytes.Buffer
	code := doAgentAttach(f, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !f.Running {
		t.Error("agent should be started before attach")
	}
	// Verify attach was called.
	attachCalled := false
	for _, c := range f.Calls {
		if c.Method == "Attach" {
			attachCalled = true
		}
	}
	if !attachCalled {
		t.Error("Attach was not called")
	}
	if !strings.Contains(stdout.String(), "Attaching to agent 'mayor'") {
		t.Errorf("stdout = %q, want attach message", stdout.String())
	}
}

func TestDoAgentAttachSkipsStartWhenRunning(t *testing.T) {
	f := agent.NewFake("mayor", "mayor")
	f.Running = true

	var stdout, stderr bytes.Buffer
	code := doAgentAttach(f, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	// Should NOT call Start since already running.
	for _, c := range f.Calls {
		if c.Method == "Start" {
			t.Error("Start should not be called when already running")
		}
	}
}

func TestDoAgentAttachStartFailure(t *testing.T) {
	f := agent.NewFake("mayor", "mayor")
	f.StartErr = errors.New("tmux crashed")

	var stdout, stderr bytes.Buffer
	code := doAgentAttach(f, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "tmux crashed") {
		t.Errorf("stderr = %q, want error message", stderr.String())
	}
}

func TestDoAgentAttachFailure(t *testing.T) {
	f := agent.NewFake("mayor", "mayor")
	f.AttachErr = errors.New("terminal gone")

	var stdout, stderr bytes.Buffer
	code := doAgentAttach(f, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "terminal gone") {
		t.Errorf("stderr = %q, want error message", stderr.String())
	}
}

// ---------------------------------------------------------------------------
// doAgentList — pool annotation (no existing coverage)
// ---------------------------------------------------------------------------

func TestDoAgentListPoolAnnotation(t *testing.T) {
	fs := fsys.NewFake()
	fs.Files["/city/city.toml"] = []byte(`[workspace]
name = "test-city"

[[agents]]
name = "worker"
[agents.pool]
min = 2
max = 5
`)

	var stdout, stderr bytes.Buffer
	code := doAgentList(fs, "/city", "", &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "pool:") {
		t.Errorf("stdout should annotate pool config: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "min=2") {
		t.Errorf("stdout should show min: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "max=5") {
		t.Errorf("stdout should show max: %q", stdout.String())
	}
}

// ---------------------------------------------------------------------------
// doAgentSuspend/Resume — bad config error path (no existing coverage)
// ---------------------------------------------------------------------------

func TestDoAgentSuspendBadConfig(t *testing.T) {
	fs := fsys.NewFake()
	fs.Files["/city/city.toml"] = []byte(`invalid ][`)

	var stdout, stderr bytes.Buffer
	code := doAgentSuspend(fs, "/city", "mayor", &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Error("stderr should contain error message")
	}
}

func TestDoAgentResumeBadConfig(t *testing.T) {
	fs := fsys.NewFake()
	fs.Files["/city/city.toml"] = []byte(`invalid ][`)

	var stdout, stderr bytes.Buffer
	code := doAgentResume(fs, "/city", "mayor", &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if stderr.Len() == 0 {
		t.Error("stderr should contain error message")
	}
}

// ---------------------------------------------------------------------------
// doAgentAdd — qualified name with dir component (no existing coverage)
// ---------------------------------------------------------------------------

func TestDoAgentAddQualifiedName(t *testing.T) {
	fs := fsys.NewFake()
	fs.Files["/city/city.toml"] = []byte(`[workspace]
name = "test-city"
`)

	var stdout, stderr bytes.Buffer
	code := doAgentAdd(fs, "/city", "myrig/worker", "", "", false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr: %s", code, stderr.String())
	}
	data := string(fs.Files["/city/city.toml"])
	if !strings.Contains(data, "worker") {
		t.Errorf("city.toml should contain agent name: %s", data)
	}
	if !strings.Contains(data, "myrig") {
		t.Errorf("city.toml should contain dir from qualified name: %s", data)
	}
}

// ---------------------------------------------------------------------------
// doAgentPeek — lines parameter (no existing coverage)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Pack-preservation tests: write-back must NOT expand includes
// ---------------------------------------------------------------------------

// packConfigWithFragment sets up a fake FS with a city.toml that uses
// include = [...] pointing to a fragment file with agents. Returns the FS.
func packConfigWithFragment(t *testing.T) fsys.Fake {
	t.Helper()
	fs := fsys.NewFake()
	// City config with include directive and one inline agent.
	// include must be top-level (before any [section] header).
	fs.Files["/city/city.toml"] = []byte(`include = ["packs/mypack/agents.toml"]

[workspace]
name = "test-city"

[[agents]]
name = "inline-agent"
`)
	// Fragment that defines a pack-derived agent.
	fs.Files["/city/packs/mypack/agents.toml"] = []byte(`[[agents]]
name = "pack-worker"
dir = "myrig"
`)
	return *fs
}

// assertConfigPreserved checks the written city.toml still has the include
// directive and does NOT contain the pack-derived agent name.
func assertConfigPreserved(t *testing.T, fs *fsys.Fake, tomlPath string) {
	t.Helper()
	data := string(fs.Files[tomlPath])
	if !strings.Contains(data, "packs/mypack/agents.toml") {
		t.Errorf("city.toml should preserve include directive:\n%s", data)
	}
	if strings.Contains(data, "pack-worker") {
		t.Errorf("city.toml should NOT contain expanded pack agent:\n%s", data)
	}
}

func TestDoAgentAddPreservesConfig(t *testing.T) {
	fs := packConfigWithFragment(t)

	var stdout, stderr bytes.Buffer
	code := doAgentAdd(&fs, "/city", "new-agent", "", "", false, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr: %s", code, stderr.String())
	}
	assertConfigPreserved(t, &fs, "/city/city.toml")
	// New agent should be present.
	data := string(fs.Files["/city/city.toml"])
	if !strings.Contains(data, "new-agent") {
		t.Errorf("city.toml should contain new agent:\n%s", data)
	}
}

func TestDoAgentSuspendInlinePreservesConfig(t *testing.T) {
	fs := packConfigWithFragment(t)

	var stdout, stderr bytes.Buffer
	code := doAgentSuspend(&fs, "/city", "inline-agent", &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0; stderr: %s", code, stderr.String())
	}
	assertConfigPreserved(t, &fs, "/city/city.toml")
	data := string(fs.Files["/city/city.toml"])
	if !strings.Contains(data, "suspended = true") {
		t.Errorf("city.toml should contain suspended = true:\n%s", data)
	}
}

func TestDoAgentSuspendPackDerivedError(t *testing.T) {
	fs := packConfigWithFragment(t)

	var stdout, stderr bytes.Buffer
	code := doAgentSuspend(&fs, "/city", "myrig/pack-worker", &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1 for pack-derived agent", code)
	}
	errMsg := stderr.String()
	if !strings.Contains(errMsg, "defined by a pack") {
		t.Errorf("stderr should mention pack: %s", errMsg)
	}
	if !strings.Contains(errMsg, "[[patches]]") {
		t.Errorf("stderr should mention patches: %s", errMsg)
	}
	// Config must NOT have been modified.
	assertConfigPreserved(t, &fs, "/city/city.toml")
}

func TestDoAgentResumePackDerivedError(t *testing.T) {
	fs := packConfigWithFragment(t)

	var stdout, stderr bytes.Buffer
	code := doAgentResume(&fs, "/city", "myrig/pack-worker", &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d, want 1 for pack-derived agent", code)
	}
	errMsg := stderr.String()
	if !strings.Contains(errMsg, "defined by a pack") {
		t.Errorf("stderr should mention pack: %s", errMsg)
	}
	if !strings.Contains(errMsg, "[[patches]]") {
		t.Errorf("stderr should mention patches: %s", errMsg)
	}
}

// ---------------------------------------------------------------------------
// doAgentPeek — lines parameter (no existing coverage)
// ---------------------------------------------------------------------------

func TestDoAgentPeekPassesLineCount(t *testing.T) {
	f := agent.NewFake("mayor", "mayor")
	f.FakePeekOutput = "output"

	var stdout, stderr bytes.Buffer
	code := doAgentPeek(f, 100, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	// Verify Peek was called with the right line count.
	for _, c := range f.Calls {
		if c.Method == "Peek" && c.Lines != 100 {
			t.Errorf("Peek called with lines=%d, want 100", c.Lines)
		}
	}
}
