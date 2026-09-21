package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fslock"
	"github.com/gastownhall/gascity/internal/runtime"
)

func TestStartAdmissionConcurrentManagersShareCityLimit(t *testing.T) {
	const limit = 3
	ceiling := limit
	cfg := &config.City{Workspace: config.Workspace{MaxActiveSessions: &ceiling}}
	store, sp, city := beads.NewMemStore(), runtime.NewFake(), t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			gate := NewStartAdmission(city, cfg, store, sp)
			errs <- gate.Start(context.Background(), sp, fmt.Sprintf("manual-%d", i), runtime.Config{})
		}(i)
	}
	wg.Wait()
	close(errs)
	accepted := 0
	for err := range errs {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrStartDeferred) {
			t.Fatal(err)
		}
	}
	running, _ := sp.ListRunning("")
	if accepted != limit || len(running) != limit {
		t.Fatalf("accepted=%d running=%d, want %d", accepted, len(running), limit)
	}
}

func TestStartAdmissionCrossProcessLock(t *testing.T) {
	city := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartAdmissionLockChild$")
	child.Env = append(os.Environ(), "GC_ADMISSION_TEST_CITY="+city)
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = child.Wait() })
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("child lock readiness: %q %v", line, err)
	}
	ceiling := 1
	sp := runtime.NewFake()
	gate := NewStartAdmission(city, &config.City{Workspace: config.Workspace{MaxActiveSessions: &ceiling}}, beads.NewMemStore(), sp)
	waitCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	if err := gate.Start(waitCtx, sp, "blocked", runtime.Config{}); !errors.Is(err, ErrStartDeferred) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("launch must wait for other OS process and honor cancellation: %v", err)
	}
	if sp.IsRunning("blocked") {
		t.Fatal("cross-process admission lock bypassed")
	}
	_ = stdin.Close()
	if err := gate.Start(ctx, sp, "after-release", runtime.Config{}); err != nil {
		t.Fatalf("admission did not recover after lock holder exited: %v", err)
	}
}

func TestStartAdmissionLockChild(t *testing.T) {
	city := os.Getenv("GC_ADMISSION_TEST_CITY")
	if city == "" {
		t.Skip("subprocess fixture")
	}
	dir := citylayout.RuntimePath(city)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, "session-start-admission.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := fslock.LockEx(f); err != nil {
		t.Fatal(err)
	}
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestStartAdmissionExistingOverLimitAndDisabled(t *testing.T) {
	sp := runtime.NewFake()
	for _, name := range []string{"already-1", "already-2"} {
		if err := sp.Start(context.Background(), name, runtime.Config{}); err != nil {
			t.Fatal(err)
		}
	}
	ceiling := 1
	gate := NewStartAdmission(t.TempDir(), &config.City{Workspace: config.Workspace{MaxActiveSessions: &ceiling}}, nil, sp)
	if err := gate.Start(context.Background(), sp, "new", runtime.Config{}); !errors.Is(err, ErrStartDeferred) {
		t.Fatalf("over-limit city must refuse new launches: %v", err)
	}
	if err := gate.Start(context.Background(), sp, "already-1", runtime.Config{}); !errors.Is(err, runtime.ErrSessionExists) {
		t.Fatalf("existing identity semantics changed: %v", err)
	}
	for _, limit := range []*int{nil, new(-1)} {
		if gate := NewStartAdmission("", &config.City{Workspace: config.Workspace{MaxActiveSessions: limit}}, nil, sp); gate != nil {
			t.Fatal("disabled cap must preserve old path")
		}
	}
	running, _ := sp.ListRunning("")
	if len(running) != 2 {
		t.Fatalf("existing work changed: %v", running)
	}
}

func TestStartAdmissionExemptionsAndExistingWork(t *testing.T) {
	ceiling := 1
	cfg := &config.City{Workspace: config.Workspace{MaxActiveSessions: &ceiling, SessionLimitExemptTemplates: []string{"rig/control"}}}
	store, sp, city := beads.NewMemStore(), runtime.NewFake(), t.TempDir()
	for _, name := range []string{"control-1", "control-2"} {
		if _, err := store.Create(beads.Bead{Type: BeadType, Labels: []string{LabelSession, "template:rig/control"}, Metadata: map[string]string{"session_name": name, "template": "rig/control"}}); err != nil {
			t.Fatal(err)
		}
	}
	gate := NewStartAdmission(city, cfg, store, sp)
	for _, name := range []string{"control-1", "unknown-model", "control-2"} {
		if err := gate.Start(context.Background(), sp, name, runtime.Config{}); err != nil {
			t.Fatalf("start %s: %v", name, err)
		}
	}
	if err := gate.Start(context.Background(), sp, "another-model", runtime.Config{}); !errors.Is(err, ErrStartDeferred) {
		t.Fatalf("start at capacity: %v", err)
	}
	// Lowering a limit never stops existing sessions, including unknown ones.
	running, _ := sp.ListRunning("")
	if len(running) != 3 {
		t.Fatalf("running=%v", running)
	}
	if err := sp.Stop("unknown-model"); err != nil {
		t.Fatal(err)
	}
	if err := gate.Start(context.Background(), sp, "another-model", runtime.Config{}); err != nil {
		t.Fatalf("capacity did not recover: %v", err)
	}
}

type admissionListFailure struct{ *runtime.Fake }

func (p admissionListFailure) ListRunning(string) ([]string, error) {
	return []string{"partial"}, errors.New("runtime census incomplete")
}

func TestStartAdmissionFailsClosed(t *testing.T) {
	ceiling := 2
	cfg := &config.City{Workspace: config.Workspace{MaxActiveSessions: &ceiling}}
	sp := admissionListFailure{runtime.NewFake()}
	gate := NewStartAdmission(t.TempDir(), cfg, beads.NewMemStore(), sp)
	if err := gate.Start(context.Background(), sp, "new", runtime.Config{}); !errors.Is(err, ErrStartDeferred) {
		t.Fatalf("partial census must defer: %v", err)
	}
	if sp.IsRunning("new") {
		t.Fatal("started on incomplete census")
	}
}

type admissionStoreFailure struct{ beads.Store }

func (s admissionStoreFailure) ListByLabel(string, int, ...beads.QueryOpt) ([]beads.Bead, error) {
	return nil, errors.New("session census incomplete")
}

func TestStartAdmissionExemptionCensusFailsClosed(t *testing.T) {
	ceiling := 2
	cfg := &config.City{Workspace: config.Workspace{MaxActiveSessions: &ceiling, SessionLimitExemptTemplates: []string{"control"}}}
	sp := runtime.NewFake()
	store := admissionStoreFailure{beads.NewMemStore()}
	gate := NewStartAdmission(t.TempDir(), cfg, store, sp)
	if err := gate.Start(context.Background(), sp, "new", runtime.Config{}); !errors.Is(err, ErrStartDeferred) {
		t.Fatalf("incomplete exemption census must defer: %v", err)
	}
}

func TestStartAdmissionManagerResumePaths(t *testing.T) {
	for _, runtimeOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("runtime-only=%v", runtimeOnly), func(t *testing.T) {
			ceiling := 0
			cfg := &config.City{Workspace: config.Workspace{MaxActiveSessions: &ceiling}}
			store, sp, city := beads.NewMemStore(), runtime.NewFake(), t.TempDir()
			mgr := NewManagerWithOptions(store, sp, WithCityPath(city), WithStartAdmission(NewStartAdmission(city, cfg, store, sp)))
			info, err := mgr.CreateSession(context.Background(), CreateOptions{Template: "manual", Command: "true", Provider: "test", WorkDir: t.TempDir(), BeadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if runtimeOnly {
				err = mgr.StartRuntimeOnly(context.Background(), info.ID, "true", runtime.Config{})
			} else {
				err = mgr.Start(context.Background(), info.ID, "true", runtime.Config{})
			}
			if !errors.Is(err, ErrStartDeferred) {
				t.Fatalf("resume bypassed capacity: %v", err)
			}
			b, err := store.Get(info.ID)
			if err != nil || b.Status == "closed" || sp.IsRunning(info.SessionName) {
				t.Fatalf("deferral must preserve pending session without launch: bead=%v err=%v", b, err)
			}
		})
	}
}

func TestStartAdmissionFailureReleasesCapacity(t *testing.T) {
	ceiling := 1
	cfg := &config.City{Workspace: config.Workspace{MaxActiveSessions: &ceiling}}
	sp := runtime.NewFake()
	sp.StartErrors["bad"] = errors.New("failed launch")
	gate := NewStartAdmission(t.TempDir(), cfg, beads.NewMemStore(), sp)
	if err := gate.Start(context.Background(), sp, "bad", runtime.Config{}); err == nil || errors.Is(err, ErrStartDeferred) {
		t.Fatalf("provider failure must stay distinct: %v", err)
	}
	if err := gate.Start(context.Background(), sp, "good", runtime.Config{}); err != nil {
		t.Fatal(err)
	}
}
