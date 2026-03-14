package workspacesvc

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
)

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
		t.Fatalf("LocalState = %q, want ready (reason=%q, log=%q)", status.LocalState, status.StateReason, string(logData))
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
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path) //nolint:errcheck // test helper
	})

	srv := &http.Server{Handler: mux}
	err = srv.Serve(ln)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve: %v", err)
	}
}
