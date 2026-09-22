package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/gastownhall/gascity/internal/fsys"
)

func TestLoadWithIncludesWorkspaceAdmissionPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, overlay string
		limit         int
		exempt        []string
		source        string
	}{
		{"root", "", 32, []string{"mechanical"}, "/city/city.toml"},
		{"omitted", "[workspace]\nsuspended = false", 32, []string{"mechanical"}, "/city/city.toml"},
		{"replace", "[workspace]\nmax_active_sessions = 16\nsession_limit_exempt_templates = ['rig/mechanical']", 16, []string{"rig/mechanical"}, "/city/overlay.toml"},
		{"clear", "[workspace]\nmax_active_sessions = 0\nsession_limit_exempt_templates = []", 0, nil, "/city/overlay.toml"},
		{"unlimited", "[workspace]\nmax_active_sessions = -1\nsession_limit_exempt_templates = []", -1, nil, "/city/overlay.toml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fs := fsys.NewFake()
			fs.Files["/city/city.toml"] = []byte("[workspace]\nname = 'test'\nmax_active_sessions = 32\nsession_limit_exempt_templates = ['mechanical']")
			var extras []string
			if tc.overlay != "" {
				fs.Files["/city/overlay.toml"] = []byte(tc.overlay)
				extras = []string{"/city/overlay.toml"}
			}
			cfg, prov, err := LoadWithIncludes(fs, "/city/city.toml", extras...)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Workspace.MaxActiveSessions == nil || *cfg.Workspace.MaxActiveSessions != tc.limit {
				t.Errorf("limit = %v, want %d", cfg.Workspace.MaxActiveSessions, tc.limit)
			}
			if !reflect.DeepEqual(cfg.Workspace.SessionLimitExemptTemplates, tc.exempt) {
				t.Errorf("exemptions = %v, want %v", cfg.Workspace.SessionLimitExemptTemplates, tc.exempt)
			}
			for _, key := range []string{"max_active_sessions", "session_limit_exempt_templates"} {
				if got := filepath.ToSlash(prov.Workspace[key]); got != tc.source {
					t.Errorf("%s provenance = %q, want %q", key, got, tc.source)
				}
			}
		})
	}
}
