package session

import (
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
)

func TestEncodeMCPServersSnapshotRedactsSecrets(t *testing.T) {
	raw, err := EncodeMCPServersSnapshot([]runtime.MCPServerConfig{{
		Name:      "remote",
		Transport: runtime.MCPTransportHTTP,
		Command:   "/bin/mcp",
		Args: []string{
			"--serve",
			"--api-key",
			"super-secret",
			"--token=abc123",
			"Authorization: Bearer secret",
			"https://user:pass@example.invalid/mcp?token=abc123",
		},
		Env: map[string]string{
			"API_TOKEN": "super-secret",
		},
		URL: "https://user:pass@example.invalid/mcp?token=abc123",
		Headers: map[string]string{
			"Authorization": "Bearer secret",
		},
	}})
	if err != nil {
		t.Fatalf("EncodeMCPServersSnapshot: %v", err)
	}

	servers, err := DecodeMCPServersSnapshot(raw)
	if err != nil {
		t.Fatalf("DecodeMCPServersSnapshot: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("len(servers) = %d, want 1", len(servers))
	}
	if got, want := servers[0].Env["API_TOKEN"], redactedMCPSnapshotValue; got != want {
		t.Fatalf("Env[API_TOKEN] = %q, want %q", got, want)
	}
	if got, want := servers[0].Headers["Authorization"], redactedMCPSnapshotValue; got != want {
		t.Fatalf("Headers[Authorization] = %q, want %q", got, want)
	}
	if got, want := servers[0].Args[0], "--serve"; got != want {
		t.Fatalf("Args[0] = %q, want %q", got, want)
	}
	if got, want := servers[0].Args[2], redactedMCPSnapshotValue; got != want {
		t.Fatalf("Args[2] = %q, want %q", got, want)
	}
	if got, want := servers[0].Args[3], "--token="+redactedMCPSnapshotValue; got != want {
		t.Fatalf("Args[3] = %q, want %q", got, want)
	}
	if got, want := servers[0].Args[4], redactedMCPSnapshotValue; got != want {
		t.Fatalf("Args[4] = %q, want %q", got, want)
	}
	if got, want := servers[0].Args[5], "https://__redacted__:__redacted__@example.invalid/mcp?token="+redactedMCPSnapshotValue; got != want {
		t.Fatalf("Args[5] = %q, want %q", got, want)
	}
	if got, want := servers[0].URL, "https://__redacted__:__redacted__@example.invalid/mcp?token="+redactedMCPSnapshotValue; got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
	if !StoredMCPSnapshotContainsRedactions(servers) {
		t.Fatal("StoredMCPSnapshotContainsRedactions() = false, want true")
	}
}

func TestRuntimeMCPServersSnapshotRoundTrip(t *testing.T) {
	cityPath := t.TempDir()
	servers := []runtime.MCPServerConfig{{
		Name:      "remote",
		Transport: runtime.MCPTransportHTTP,
		Command:   "/bin/mcp",
		Args:      []string{"--api-key", "super-secret"},
		Env: map[string]string{
			"API_TOKEN": "super-secret",
		},
		URL: "https://user:pass@example.invalid/mcp?token=abc123",
		Headers: map[string]string{
			"Authorization": "Bearer secret",
		},
	}}
	if err := PersistRuntimeMCPServersSnapshot(cityPath, "sess-1", servers); err != nil {
		t.Fatalf("PersistRuntimeMCPServersSnapshot: %v", err)
	}

	loaded, err := LoadRuntimeMCPServersSnapshot(cityPath, "sess-1")
	if err != nil {
		t.Fatalf("LoadRuntimeMCPServersSnapshot: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("len(loaded) = %d, want 1", len(loaded))
	}
	if got, want := loaded[0].Args[1], "super-secret"; got != want {
		t.Fatalf("Args[1] = %q, want %q", got, want)
	}
	if got, want := loaded[0].Env["API_TOKEN"], "super-secret"; got != want {
		t.Fatalf("Env[API_TOKEN] = %q, want %q", got, want)
	}
	if got, want := loaded[0].Headers["Authorization"], "Bearer secret"; got != want {
		t.Fatalf("Headers[Authorization] = %q, want %q", got, want)
	}
}
