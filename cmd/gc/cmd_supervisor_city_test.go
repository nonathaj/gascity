package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/supervisor"
)

func withSupervisorTestHooks(t *testing.T, ensure func(stdout, stderr io.Writer) int, reload func(stdout, stderr io.Writer) int, alive func() int, running func(string) (bool, bool), timeout, poll time.Duration) {
	t.Helper()

	oldEnsure := ensureSupervisorRunningHook
	oldReload := reloadSupervisorHook
	oldAlive := supervisorAliveHook
	oldRunning := supervisorCityRunningHook
	oldTimeout := supervisorCityReadyTimeout
	oldPoll := supervisorCityPollInterval

	ensureSupervisorRunningHook = ensure
	reloadSupervisorHook = reload
	supervisorAliveHook = alive
	supervisorCityRunningHook = running
	supervisorCityReadyTimeout = timeout
	supervisorCityPollInterval = poll

	t.Cleanup(func() {
		ensureSupervisorRunningHook = oldEnsure
		reloadSupervisorHook = oldReload
		supervisorAliveHook = oldAlive
		supervisorCityRunningHook = oldRunning
		supervisorCityReadyTimeout = oldTimeout
		supervisorCityPollInterval = oldPoll
	})
}

func TestRegisterCityWithSupervisorRollsBackWhenCityNeverBecomesReady(t *testing.T) {
	gcHome := t.TempDir()
	t.Setenv("GC_HOME", gcHome)

	cityPath := filepath.Join(t.TempDir(), "bright-lights")
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"bright-lights\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	withSupervisorTestHooks(
		t,
		func(_, _ io.Writer) int { return 0 },
		func(_, _ io.Writer) int { return 0 },
		func() int { return 4242 },
		func(string) (bool, bool) { return false, true },
		20*time.Millisecond,
		time.Millisecond,
	)

	var stdout, stderr bytes.Buffer
	code := registerCityWithSupervisor(cityPath, &stdout, &stderr, "gc register")
	if code != 1 {
		t.Fatalf("registerCityWithSupervisor code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "registration rolled back") {
		t.Fatalf("stderr = %q, want rollback message", stderr.String())
	}

	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	entries, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty registry after rollback, got %v", entries)
	}
}

func TestUnregisterCityFromSupervisorRestoresRegistrationOnReloadFailure(t *testing.T) {
	gcHome := t.TempDir()
	t.Setenv("GC_HOME", gcHome)

	cityPath := filepath.Join(t.TempDir(), "bright-lights")
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"bright-lights\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	if err := reg.Register(cityPath, "bright-lights"); err != nil {
		t.Fatal(err)
	}

	withSupervisorTestHooks(
		t,
		func(_, _ io.Writer) int { return 0 },
		func(_, _ io.Writer) int { return 1 },
		func() int { return 4242 },
		func(string) (bool, bool) { return false, false },
		20*time.Millisecond,
		time.Millisecond,
	)

	var stdout, stderr bytes.Buffer
	handled, code := unregisterCityFromSupervisor(cityPath, &stdout, &stderr, "gc unregister")
	if !handled || code != 1 {
		t.Fatalf("unregisterCityFromSupervisor = (%t, %d), want (true, 1)", handled, code)
	}
	if !strings.Contains(stderr.String(), "restored registration") {
		t.Fatalf("stderr = %q, want restore message", stderr.String())
	}

	entries, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != cityPath {
		t.Fatalf("expected restored registry entry for %s, got %v", cityPath, entries)
	}
}

func TestControllerStatusForSupervisorManagedCityStopped(t *testing.T) {
	gcHome := t.TempDir()
	t.Setenv("GC_HOME", gcHome)

	cityPath := filepath.Join(t.TempDir(), "bright-lights")
	if err := os.MkdirAll(cityPath, 0o755); err != nil {
		t.Fatal(err)
	}
	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	if err := reg.Register(cityPath, "bright-lights"); err != nil {
		t.Fatal(err)
	}

	oldAlive := supervisorAliveHook
	oldRunning := supervisorCityRunningHook
	supervisorAliveHook = func() int { return 4242 }
	supervisorCityRunningHook = func(string) (bool, bool) { return false, true }
	t.Cleanup(func() {
		supervisorAliveHook = oldAlive
		supervisorCityRunningHook = oldRunning
	})

	ctrl := controllerStatusForCity(cityPath)
	if ctrl.Running || ctrl.PID != 4242 || ctrl.Mode != "supervisor" {
		t.Fatalf("controller status = %+v, want stopped supervisor PID", ctrl)
	}
}

func TestCmdStopSupervisorManagedCityStopsLegacyControllerAndBeads(t *testing.T) {
	gcHome := t.TempDir()
	t.Setenv("GC_HOME", gcHome)

	root, err := os.MkdirTemp("", "gcstop-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) }) //nolint:errcheck

	cityPath := filepath.Join(root, "city")
	if err := os.MkdirAll(filepath.Join(cityPath, ".gc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cityPath, "city.toml"), []byte("[workspace]\nname = \"bright-lights\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := supervisor.NewRegistry(supervisor.RegistryPath())
	if err := reg.Register(cityPath, "bright-lights"); err != nil {
		t.Fatal(err)
	}

	logFile := filepath.Join(t.TempDir(), "ops.log")
	script := writeSpyScript(t, logFile)
	t.Setenv("GC_BEADS", "exec:"+script)

	withSupervisorTestHooks(
		t,
		func(_, _ io.Writer) int { return 0 },
		func(_, _ io.Writer) int { return 0 },
		func() int { return 4242 },
		func(string) (bool, bool) { return false, false },
		20*time.Millisecond,
		time.Millisecond,
	)

	sockPath := filepath.Join(cityPath, ".gc", "controller.sock")
	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lis.Close() //nolint:errcheck

	stopped := make(chan struct{}, 1)
	go func() {
		conn, acceptErr := lis.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close() //nolint:errcheck
		buf := make([]byte, 32)
		n, _ := conn.Read(buf)
		if strings.Contains(string(buf[:n]), "stop") {
			stopped <- struct{}{}
		}
		conn.Write([]byte("ok\n")) //nolint:errcheck
	}()

	var stdout, stderr bytes.Buffer
	code := cmdStop([]string{cityPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("cmdStop code = %d, want 0: %s", code, stderr.String())
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("expected legacy controller stop request")
	}

	entries, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected city to be unregistered after stop, got %v", entries)
	}

	ops := readOpLog(t, logFile)
	if len(ops) != 2 {
		t.Fatalf("expected bead provider stop+shutdown, got %v", ops)
	}
	if !strings.HasPrefix(ops[0], "stop") || !strings.HasPrefix(ops[1], "shutdown") {
		t.Fatalf("unexpected bead provider ops: %v", ops)
	}
}
