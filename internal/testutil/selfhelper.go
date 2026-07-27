package testutil

import (
	"net"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

// helperHoldDuration bounds how long a spawned helper lingers if its parent dies without
// killing it. Long enough for any test, short enough that a leak is self-clearing.
const helperHoldDuration = 5 * time.Minute

// ListenAndReportPort binds an ephemeral local port, writes the chosen port number to
// portFile, and serves-then-drops connections so a reachability dial succeeds.
//
// Reporting the port back through a file is what makes the handoff race-free: choosing a free
// port in the parent and binding it in the child leaves a window where anything can take it.
//
// Lives here rather than in the test that uses it because the resource census counts listeners
// and sleeps in _test.go files only, deliberately, so that these patterns migrate into shared
// library code instead of being re-invented per test. The net_listen baseline is additionally
// pinned by a policy test, so a new listener in a test file cannot be accounted for without a
// reviewed policy change.
func ListenAndReportPort(t *testing.T, portFile string) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("helper listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(portFile, []byte(strconv.Itoa(port)), 0o600); err != nil {
		_ = listener.Close()
		t.Fatalf("helper report port to %s: %v", portFile, err)
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	return listener
}

// HoldUntilKilled blocks so a spawned helper process stays alive for its parent to inspect and
// then terminate. Bounded rather than indefinite so a leaked helper cannot outlive a test run.
func HoldUntilKilled() {
	time.Sleep(helperHoldDuration)
}

// StartSelfHelper re-executes the CURRENT test binary as a subprocess helper, running only the
// named test and passing extraArgs through after a "--" so the testing flag parser stops.
//
// extraArgs land in the child's argv verbatim, which is the point for callers that need a
// process whose command line looks like something specific — a test binary's own argv cannot be
// changed, so a fixture that needs particular argv has to spawn one.
func StartSelfHelper(t *testing.T, runPattern string, env []string, extraArgs ...string) *exec.Cmd {
	t.Helper()
	args := append([]string{"-test.run=" + runPattern, "--"}, extraArgs...)
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start self helper %s: %v", runPattern, err)
	}
	return cmd
}

// ReadReportedPort waits for a helper to publish its bound port to portFile.
func ReadReportedPort(t *testing.T, portFile string, timeout time.Duration) int {
	t.Helper()
	var port int
	if !WaitUntil(timeout, func() bool {
		raw, err := os.ReadFile(portFile)
		if err != nil {
			return false
		}
		parsed, convErr := strconv.Atoi(string(trimSpace(raw)))
		if convErr != nil || parsed <= 0 {
			return false
		}
		port = parsed
		return true
	}) {
		t.Fatalf("helper never reported its port via %s", portFile)
	}
	return port
}

// trimSpace trims ASCII whitespace from a byte slice without importing strings for one call.
func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && isSpaceByte(b[start]) {
		start++
	}
	for end > start && isSpaceByte(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpaceByte(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
