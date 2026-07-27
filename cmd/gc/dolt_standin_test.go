package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/testutil"
)

const (
	// doltStandInPortFileEnv names the file the stand-in writes its bound port to.
	doltStandInPortFileEnv = "GC_TEST_DOLT_STANDIN_PORT_FILE"
	// doltStandInHelperPattern selects the helper when the binary re-executes itself.
	doltStandInHelperPattern = "^TestDoltStandInHelper$"
)

// TestDoltStandInHelper is not a test. It is the process a fixture spawns to stand in for a
// managed dolt server, and it skips unless the environment marks it as the helper.
//
// It exists because ownership inspection reads a process's ARGV to decide whether a pid is our
// dolt (does it name our --config?), and it compares the pid holding the dolt port against the
// pid in dolt-state.json. A faithful stand-in therefore has to do both things at once: hold the
// port AND carry dolt-like argv. `os.Getpid()` cannot satisfy the second — a test binary's argv
// is fixed — which is why fixtures that used it only ever passed through a pid-equality
// shortcut that skipped inspection entirely.
//
// The port is bound as :0 and reported back through a file rather than being chosen by the
// parent and passed in. Picking a free port in the parent and binding it in the child is a
// race: anything can take the port in between.
func TestDoltStandInHelper(t *testing.T) {
	portFile := strings.TrimSpace(os.Getenv(doltStandInPortFileEnv))
	if portFile == "" {
		t.Skip("subprocess helper; runs only when the parent marks it")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("stand-in listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(port)), 0o600); err != nil {
		t.Fatalf("stand-in report port: %v", err)
	}
	// Accept and drop connections so doltPortReachable's dial succeeds rather than hanging on
	// a full backlog.
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	// Hold until the parent kills us. Bounded so a leaked helper cannot outlive a test run by
	// more than this.
	time.Sleep(5 * time.Minute)
}

// doltStandIn describes a spawned stand-in for managed dolt.
type doltStandIn struct {
	PID  int
	Port int
}

// startDoltStandIn spawns a process that stands in for managed dolt for the given city: it
// holds a TCP port and carries `--config <the city's dolt config>` in its argv, so
// managedDoltRuntimeProcessOwned can verify ownership instead of taking a pid match on trust.
//
// Returns its pid and the port it bound, for writing into dolt-state.json.
func startDoltStandIn(t *testing.T, cityPath string) doltStandIn {
	t.Helper()
	layout, err := resolveManagedDoltRuntimeLayout(cityPath)
	if err != nil {
		t.Fatalf("resolve managed dolt layout for %q: %v", cityPath, err)
	}
	return startDoltStandInForConfig(t, layout.ConfigFile)
}

// startDoltStandInForConfig is startDoltStandIn for callers that already know the config path.
func startDoltStandInForConfig(t *testing.T, configFile string) doltStandIn {
	t.Helper()
	portFile := filepath.Join(t.TempDir(), "standin.port")
	// `--` first so the testing flag parser stops, then dolt-shaped arguments that ownership
	// inspection will read out of our argv.
	cmd := exec.Command(os.Args[0],
		"-test.run="+doltStandInHelperPattern,
		"--",
		"sql-server",
		"--config="+filepath.ToSlash(configFile),
	)
	cmd.Env = append(os.Environ(), doltStandInPortFileEnv+"="+portFile)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dolt stand-in: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = pidutil.KillTree(pid)
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	go func() { _ = cmd.Wait() }()

	var port int
	if !testutil.WaitUntil(30*time.Second, func() bool {
		raw, readErr := os.ReadFile(portFile)
		if readErr != nil {
			return false
		}
		parsed, convErr := strconv.Atoi(strings.TrimSpace(string(raw)))
		if convErr != nil || parsed <= 0 {
			return false
		}
		port = parsed
		return true
	}) {
		t.Fatalf("dolt stand-in never reported its port via %s", portFile)
	}
	return doltStandIn{PID: pid, Port: port}
}
