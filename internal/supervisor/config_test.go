package supervisor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadConfigMissing(t *testing.T) {
	cfg, err := LoadConfig("/nonexistent/supervisor.toml")
	if err != nil {
		t.Fatal(err)
	}
	// Defaults should apply.
	if cfg.Supervisor.PortOrDefault() != 8372 {
		t.Errorf("expected default port 8372, got %d", cfg.Supervisor.PortOrDefault())
	}
	if cfg.Supervisor.BindOrDefault() != "127.0.0.1" {
		t.Errorf("expected default bind 127.0.0.1, got %s", cfg.Supervisor.BindOrDefault())
	}
	if cfg.Supervisor.PatrolIntervalDuration() != 10*time.Second {
		t.Errorf("expected default patrol 10s, got %v", cfg.Supervisor.PatrolIntervalDuration())
	}
}

func TestLoadConfigExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "supervisor.toml")
	if err := os.WriteFile(path, []byte(`
[supervisor]
port = 9090
bind = "0.0.0.0"
patrol_interval = "5s"
`), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Supervisor.PortOrDefault() != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Supervisor.PortOrDefault())
	}
	if cfg.Supervisor.BindOrDefault() != "0.0.0.0" {
		t.Errorf("expected bind 0.0.0.0, got %s", cfg.Supervisor.BindOrDefault())
	}
	if cfg.Supervisor.PatrolIntervalDuration() != 5*time.Second {
		t.Errorf("expected patrol 5s, got %v", cfg.Supervisor.PatrolIntervalDuration())
	}
}

func TestDefaultHomeWithEnv(t *testing.T) {
	t.Setenv("GC_HOME", "/custom/gc")
	if got := DefaultHome(); got != "/custom/gc" {
		t.Errorf("expected /custom/gc, got %s", got)
	}
}

func TestRuntimeDirWithXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if got := RuntimeDir(); got != "/run/user/1000/gc" {
		t.Errorf("expected /run/user/1000/gc, got %s", got)
	}
}

func TestRuntimeDirFallback(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := RuntimeDir()
	expected := DefaultHome()
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}
