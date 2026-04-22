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
		Args:      []string{"--serve"},
		Env: map[string]string{
			"API_TOKEN": "super-secret",
		},
		URL: "https://example.invalid/mcp",
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
	if !StoredMCPSnapshotContainsRedactions(servers) {
		t.Fatal("StoredMCPSnapshotContainsRedactions() = false, want true")
	}
}
