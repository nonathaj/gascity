package session

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gastownhall/gascity/internal/runtime"
)

const (
	// MCPIdentityMetadataKey stores the stable identity used to materialize
	// MCP templates for a session.
	MCPIdentityMetadataKey = "mcp_identity"
	// MCPServersSnapshotMetadataKey stores the normalized ACP session/new MCP
	// server snapshot used to resume sessions when the current catalog cannot
	// be materialized.
	MCPServersSnapshotMetadataKey = "mcp_servers_snapshot"

	redactedMCPSnapshotValue = "__redacted__"
)

// EncodeMCPServersSnapshot returns the normalized metadata value for a
// session's persisted ACP session/new MCP server snapshot.
func EncodeMCPServersSnapshot(servers []runtime.MCPServerConfig) (string, error) {
	normalized := normalizeMCPServersSnapshotForMetadata(servers)
	if len(normalized) == 0 {
		return "", nil
	}
	data, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal MCP server snapshot: %w", err)
	}
	return string(data), nil
}

// DecodeMCPServersSnapshot parses a persisted ACP session/new MCP server
// snapshot from session metadata.
func DecodeMCPServersSnapshot(raw string) ([]runtime.MCPServerConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var servers []runtime.MCPServerConfig
	if err := json.Unmarshal([]byte(raw), &servers); err != nil {
		return nil, fmt.Errorf("unmarshal MCP server snapshot: %w", err)
	}
	return runtime.NormalizeMCPServerConfigs(servers), nil
}

// StoredMCPSnapshotContainsRedactions reports whether a decoded persisted MCP
// snapshot contains redacted secret values and therefore cannot be used as a
// complete runtime fallback.
func StoredMCPSnapshotContainsRedactions(servers []runtime.MCPServerConfig) bool {
	for _, server := range servers {
		if snapshotMapContainsRedactions(server.Env) || snapshotMapContainsRedactions(server.Headers) {
			return true
		}
	}
	return false
}

// WithStoredMCPMetadata returns a metadata map augmented with the stable MCP
// identity and normalized ACP session/new snapshot for the session.
func WithStoredMCPMetadata(meta map[string]string, identity string, servers []runtime.MCPServerConfig) (map[string]string, error) {
	if meta == nil {
		meta = make(map[string]string)
	}
	identity = strings.TrimSpace(identity)
	if identity != "" {
		meta[MCPIdentityMetadataKey] = identity
	}
	snapshot, err := EncodeMCPServersSnapshot(servers)
	if err != nil {
		return nil, err
	}
	if snapshot != "" {
		meta[MCPServersSnapshotMetadataKey] = snapshot
	} else if _, ok := meta[MCPServersSnapshotMetadataKey]; ok {
		meta[MCPServersSnapshotMetadataKey] = ""
	}
	return meta, nil
}

func normalizeMCPServersSnapshotForMetadata(servers []runtime.MCPServerConfig) []runtime.MCPServerConfig {
	normalized := runtime.NormalizeMCPServerConfigs(servers)
	for i := range normalized {
		normalized[i].Env = redactMCPMetadataMap(normalized[i].Env)
		normalized[i].Headers = redactMCPMetadataMap(normalized[i].Headers)
	}
	return normalized
}

func redactMCPMetadataMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key := range in {
		out[key] = redactedMCPSnapshotValue
	}
	return out
}

func snapshotMapContainsRedactions(in map[string]string) bool {
	for _, value := range in {
		if value == redactedMCPSnapshotValue {
			return true
		}
	}
	return false
}
