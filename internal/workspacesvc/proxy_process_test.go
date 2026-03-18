package workspacesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/supervisor"
)

const proxyProcessPythonHelper = `
import json
import os
import socketserver
from http.server import BaseHTTPRequestHandler

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/healthz":
            self.send_response(204)
            self.end_headers()
            return
        if self.path == "/env":
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(json.dumps({
                "GC_SERVICE_PUBLIC_URL": os.environ.get("GC_SERVICE_PUBLIC_URL", ""),
            }).encode("utf-8"))
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, format, *args):
        return

sock = os.environ["GC_SERVICE_SOCKET"]
try:
    os.unlink(sock)
except FileNotFoundError:
    pass

class Server(socketserver.UnixStreamServer):
    allow_reuse_address = True

Server(sock, Handler).serve_forever()
`

func TestManagerReloadProxyProcessStartsAndProxies(t *testing.T) {
	t.Setenv("GC_SERVICE_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Services: []config.Service{{
				Name: "bridge",
				Kind: "proxy_process",
				Process: config.ServiceProcessConfig{
					Command:    []string{exe, "-test.run=^TestProxyProcessHelper$", "--"},
					HealthPath: "/healthz",
				},
			}},
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	defer mgr.Close() //nolint:errcheck // best-effort cleanup

	status, ok := mgr.Get("bridge")
	if !ok {
		t.Fatal("service status missing")
	}
	if status.LocalState != "ready" {
		logData, _ := os.ReadFile(filepath.Join(rt.cityPath, ".gc", "services", "bridge", "logs", "service.log"))
		t.Fatalf("LocalState = %q, want ready (reason=%q, log=%q)", status.LocalState, status.Reason, string(logData))
	}

	req := httptest.NewRequest(http.MethodPost, "/svc/bridge/hooks/example", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	if ok := mgr.ServeHTTP(rec, req); !ok {
		t.Fatal("ServeHTTP returned false, want true")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if strings.TrimSpace(rec.Body.String()) != "POST /hooks/example" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "POST /hooks/example")
	}
}

func TestProxyProcessHelper(t *testing.T) {
	if os.Getenv("GC_SERVICE_HELPER") != "1" {
		t.Skip("helper process")
	}
	socketPath := os.Getenv("GC_SERVICE_SOCKET")
	if socketPath == "" {
		t.Fatal("GC_SERVICE_SOCKET not set")
	}
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	defer ln.Close() //nolint:errcheck // best-effort cleanup

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/env", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"GC_CITY_ROOT":              os.Getenv("GC_CITY_ROOT"),
			"GC_CITY_RUNTIME_DIR":       os.Getenv("GC_CITY_RUNTIME_DIR"),
			"GC_SERVICE_NAME":           os.Getenv("GC_SERVICE_NAME"),
			"GC_SERVICE_STATE_ROOT":     os.Getenv("GC_SERVICE_STATE_ROOT"),
			"GC_SERVICE_PUBLIC_URL":     os.Getenv("GC_SERVICE_PUBLIC_URL"),
			"GC_SERVICE_VISIBILITY":     os.Getenv("GC_SERVICE_VISIBILITY"),
			"GC_PUBLISHED_SERVICES_DIR": os.Getenv("GC_PUBLISHED_SERVICES_DIR"),
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path) //nolint:errcheck // test helper
	})

	srv := &http.Server{Handler: mux}
	err = srv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve: %v", err)
	}
}

func TestProxyProcessPublishesServiceEnv(t *testing.T) {
	t.Setenv("GC_SERVICE_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Workspace: config.Workspace{Name: "demo-app"},
			Services: []config.Service{{
				Name: "bridge",
				Kind: "proxy_process",
				Publication: config.ServicePublicationConfig{
					Visibility: "public",
				},
				Process: config.ServiceProcessConfig{
					Command:    []string{exe, "-test.run=^TestProxyProcessHelper$", "--"},
					HealthPath: "/healthz",
				},
			}},
		},
		pubCfg: supervisor.PublicationConfig{
			Provider:         "hosted",
			TenantSlug:       "acme",
			PublicBaseDomain: "apps.example.com",
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	defer mgr.Close() //nolint:errcheck // best-effort cleanup

	req := httptest.NewRequest(http.MethodGet, "/svc/bridge/env", nil)
	rec := httptest.NewRecorder()
	if ok := mgr.ServeHTTP(rec, req); !ok {
		t.Fatal("ServeHTTP returned false, want true")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var env map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode env: %v", err)
	}
	if env["GC_CITY_ROOT"] != rt.cityPath {
		t.Fatalf("GC_CITY_ROOT = %q, want %q", env["GC_CITY_ROOT"], rt.cityPath)
	}
	if env["GC_CITY_RUNTIME_DIR"] != filepath.Join(rt.cityPath, ".gc", "runtime") {
		t.Fatalf("GC_CITY_RUNTIME_DIR = %q, want %q", env["GC_CITY_RUNTIME_DIR"], filepath.Join(rt.cityPath, ".gc", "runtime"))
	}
	if env["GC_SERVICE_NAME"] != "bridge" {
		t.Fatalf("GC_SERVICE_NAME = %q, want bridge", env["GC_SERVICE_NAME"])
	}
	if env["GC_SERVICE_STATE_ROOT"] != filepath.Join(rt.cityPath, ".gc", "services", "bridge") {
		t.Fatalf("GC_SERVICE_STATE_ROOT = %q, want %q", env["GC_SERVICE_STATE_ROOT"], filepath.Join(rt.cityPath, ".gc", "services", "bridge"))
	}
	if !strings.HasPrefix(env["GC_SERVICE_PUBLIC_URL"], "https://bridge--demo-app--acme--") {
		t.Fatalf("GC_SERVICE_PUBLIC_URL = %q, want bridge--demo-app--acme prefix", env["GC_SERVICE_PUBLIC_URL"])
	}
	if env["GC_SERVICE_VISIBILITY"] != "public" {
		t.Fatalf("GC_SERVICE_VISIBILITY = %q, want public", env["GC_SERVICE_VISIBILITY"])
	}
	if env["GC_PUBLISHED_SERVICES_DIR"] != citylayout.PublishedServicesDir(rt.cityPath) {
		t.Fatalf("GC_PUBLISHED_SERVICES_DIR = %q, want %q", env["GC_PUBLISHED_SERVICES_DIR"], citylayout.PublishedServicesDir(rt.cityPath))
	}
}

func TestProxyProcessReloadRefreshesPublicationEnv(t *testing.T) {
	t.Setenv("GC_SERVICE_HELPER", "1")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}

	rt := &testRuntime{
		cityPath: t.TempDir(),
		cityName: "test-city",
		cfg: &config.City{
			Workspace: config.Workspace{Name: "demo-app"},
			Services: []config.Service{{
				Name: "bridge",
				Kind: "proxy_process",
				Publication: config.ServicePublicationConfig{
					Visibility: "public",
				},
				Process: config.ServiceProcessConfig{
					Command:    []string{exe, "-test.run=^TestProxyProcessHelper$", "--"},
					HealthPath: "/healthz",
				},
			}},
		},
		pubCfg: supervisor.PublicationConfig{
			Provider:         "hosted",
			TenantSlug:       "acme",
			PublicBaseDomain: "apps.example.com",
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("first Reload: %v", err)
	}
	defer mgr.Close() //nolint:errcheck // best-effort cleanup

	loadEnv := func() map[string]string {
		deadline := time.Now().Add(2 * time.Second)
		for {
			req := httptest.NewRequest(http.MethodGet, "/svc/bridge/env", nil)
			rec := httptest.NewRecorder()
			if ok := mgr.ServeHTTP(rec, req); ok && rec.Code == http.StatusOK {
				var env map[string]string
				if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
					t.Fatalf("decode env: %v", err)
				}
				return env
			}
			if time.Now().After(deadline) {
				status, _ := mgr.Get("bridge")
				logData, _ := os.ReadFile(filepath.Join(rt.cityPath, ".gc", "services", "bridge", "logs", "service.log"))
				t.Fatalf("timed out waiting for proxy process env endpoint: status=%+v log=%q", status, string(logData))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	first := loadEnv()
	rt.pubCfg.TenantSlug = "beta"
	if err := mgr.Reload(); err != nil {
		t.Fatalf("second Reload: %v", err)
	}
	second := loadEnv()

	if first["GC_SERVICE_PUBLIC_URL"] == second["GC_SERVICE_PUBLIC_URL"] {
		t.Fatalf("GC_SERVICE_PUBLIC_URL did not change across reload: %q", first["GC_SERVICE_PUBLIC_URL"])
	}
	if !strings.Contains(second["GC_SERVICE_PUBLIC_URL"], "--beta--") {
		t.Fatalf("GC_SERVICE_PUBLIC_URL = %q, want beta route", second["GC_SERVICE_PUBLIC_URL"])
	}
}

func TestProxyProcessTickRefreshesPublicationEnvFromAuthoritativeStore(t *testing.T) {
	cityPath := filepath.Join(os.TempDir(), fmt.Sprintf("gc-tick-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", cityPath, err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(cityPath)
	})

	rt := &testRuntime{
		cityPath: cityPath,
		cityName: "test-city",
		cfg: &config.City{
			Workspace: config.Workspace{Name: "demo-app"},
			Services: []config.Service{{
				Name: "bridge",
				Kind: "proxy_process",
				Publication: config.ServicePublicationConfig{
					Visibility: "public",
				},
				Process: config.ServiceProcessConfig{
					Command:    []string{"python3", "-c", proxyProcessPythonHelper},
					HealthPath: "/healthz",
				},
			}},
		},
		pubCfg: supervisor.PublicationConfig{
			Provider:         "hosted",
			TenantSlug:       "acme",
			PublicBaseDomain: "apps.example.com",
		},
		sp:    runtime.NewFake(),
		store: beads.NewMemStore(),
	}

	mgr := NewManager(rt)
	if err := mgr.Reload(); err != nil {
		t.Fatalf("first Reload: %v", err)
	}
	defer mgr.Close() //nolint:errcheck // best-effort cleanup

	loadEnv := func() map[string]string {
		deadline := time.Now().Add(2 * time.Second)
		for {
			req := httptest.NewRequest(http.MethodGet, "/svc/bridge/env", nil)
			rec := httptest.NewRecorder()
			if ok := mgr.ServeHTTP(rec, req); ok && rec.Code == http.StatusOK {
				var env map[string]string
				if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
					t.Fatalf("decode env: %v", err)
				}
				return env
			}
			if time.Now().After(deadline) {
				status, _ := mgr.Get("bridge")
				logData, _ := os.ReadFile(filepath.Join(rt.cityPath, ".gc", "services", "bridge", "logs", "service.log"))
				t.Fatalf("timed out waiting for proxy process env endpoint: status=%+v log=%q", status, string(logData))
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	first := loadEnv()
	writePublicationStoreForTest(t, rt.cityPath, `[
  {
    "service_name": "bridge",
    "visibility": "public",
    "url": "https://bridge--acme--deadbeef.apps.example.com"
  }
]`)
	refs := loadPublicationRefs(rt.PublicationStorePath(), rt.CityPath())
	if refs.err != nil {
		t.Fatalf("loadPublicationRefs: %v", refs.err)
	}
	if refs.refs["bridge"].URL != "https://bridge--acme--deadbeef.apps.example.com" {
		t.Fatalf("authoritative ref URL = %q, want stored route", refs.refs["bridge"].URL)
	}

	mgr.Tick(context.Background(), time.Now().UTC())
	second := loadEnv()

	if first["GC_SERVICE_PUBLIC_URL"] == second["GC_SERVICE_PUBLIC_URL"] {
		t.Fatalf("GC_SERVICE_PUBLIC_URL did not change across tick: %q", first["GC_SERVICE_PUBLIC_URL"])
	}
	if second["GC_SERVICE_PUBLIC_URL"] != "https://bridge--acme--deadbeef.apps.example.com" {
		t.Fatalf("GC_SERVICE_PUBLIC_URL = %q, want authoritative route", second["GC_SERVICE_PUBLIC_URL"])
	}
}
