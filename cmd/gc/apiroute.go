package main

import (
	"fmt"
	"path/filepath"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fsys"
)

// apiClient returns an API client if a controller with an API server is
// running for the city at cityPath. Returns nil if no controller is running
// or the API is not configured. CLI commands use this to route writes through
// the API when available, falling back to direct file mutation.
func apiClient(cityPath string) *api.Client {
	// Check if controller is alive.
	if controllerAlive(cityPath) == 0 {
		return nil
	}

	// Load config to find API port.
	tomlPath := filepath.Join(cityPath, "city.toml")
	cfg, err := config.Load(fsys.OSFS{}, tomlPath)
	if err != nil {
		return nil
	}
	if cfg.API.Port == 0 {
		return nil
	}

	bind := cfg.API.BindOrDefault()
	baseURL := fmt.Sprintf("http://%s:%d", bind, cfg.API.Port)
	return api.NewClient(baseURL)
}
