package api

import (
	"context"
	"errors"
	"testing"

	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/worker"
)

func TestAPISessionAdmissionCannotBypassCityLimit(t *testing.T) {
	fs := newSessionFakeState(t)
	ceiling := 0
	fs.cfg.Workspace.MaxActiveSessions = &ceiling
	srv := New(fs)
	factory, err := srv.workerFactory(fs.cityBeadStore)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := factory.Session(worker.SessionSpec{Template: "manual", Command: "true", Provider: "test", WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.Create(context.Background(), worker.CreateModeStarted); !errors.Is(err, session.ErrStartDeferred) {
		t.Fatalf("API worker create must defer: %v", err)
	}
	mgr := srv.sessionManager(fs.cityBeadStore)
	if _, err := mgr.CreateSession(context.Background(), session.CreateOptions{Template: "manual", Command: "true", Provider: "test", WorkDir: t.TempDir(), Hints: runtime.Config{}}); !errors.Is(err, session.ErrStartDeferred) {
		t.Fatalf("API compatibility manager must defer: %v", err)
	}
}
