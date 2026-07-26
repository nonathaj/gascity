package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/execshim"
)

func skipSlowCmdGCTest(t *testing.T, reason string) {
	t.Helper()
	if testing.Short() || strings.TrimSpace(os.Getenv("GC_FAST_UNIT")) != "0" {
		if strings.TrimSpace(os.Getenv("GC_FAST_UNIT")) == "" && !strings.Contains(reason, "test-cmd-gc-process") {
			reason += "; set GC_FAST_UNIT=0 or run make test-cmd-gc-process for full process coverage"
		}
		t.Skip(reason)
	}
}

// sanitizedBaseEnv returns os.Environ() with every GC_*/BEADS_* entry
// filtered out, followed by the given extras. Use this to build the
// `Env` for any exec.Cmd that runs the real gc-beads-bd lifecycle script
// or gc subcommands — inheriting os.Environ() raw lets GC_CITY_RUNTIME_DIR,
// GC_PACK_STATE_DIR, GC_DOLT_STATE_FILE, and friends point the child at
// the user's real registered city instead of the test's t.TempDir(),
// which silently overwrites user state on every run.
// Regression for gastownhall/gascity#938.
func sanitizedBaseEnv(extra ...string) []string {
	// When the caller overrides PATH, drop the ambient entry so the child env
	// carries exactly one PATH and EnvWithShellDir below patches the effective
	// one (duplicate keys resolve last-wins on Windows, which would otherwise
	// discard the injection).
	overridesPath := false
	for _, kv := range extra {
		if len(kv) >= 5 && strings.EqualFold(kv[:5], "PATH=") {
			overridesPath = true
			break
		}
	}
	filtered := make([]string, 0, len(os.Environ()))
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GC_") || strings.HasPrefix(kv, "BEADS_") {
			continue
		}
		if overridesPath && len(kv) >= 5 && strings.EqualFold(kv[:5], "PATH=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	filtered = append(filtered,
		managedDoltTestModeEnv+"=1",
		managedDoltTestParentPIDEnv+"="+strconv.Itoa(os.Getpid()),
	)
	// The scripts these envs feed run under sh via execshim; keep Git for
	// Windows' coreutils (dirname, mkdir, ...) resolvable exactly as
	// execshim.Command would have (the cmd.Env overwrite discards its
	// default injection). Identity on Unix.
	return execshim.EnvWithShellDir(append(filtered, extra...))
}

// absFixture turns a rootless POSIX fixture path ("/city") into a genuinely
// absolute path on every platform. On Windows "/city" is NOT absolute (no
// volume), so production path resolution treats it as relative and joins it
// under other roots, silently changing what the test exercises (doctrine T2;
// same helper as internal/supervisor).
func absFixture(p string) string {
	if runtime.GOOS == "windows" {
		return filepath.FromSlash("C:" + p)
	}
	return p
}

// installFakeToolOnPath writes a fake sh tool (bd, dolt, gc, ...) into binDir
// so BOTH invocation routes resolve it: the extensionless file serves sh PATH
// lookup (provider scripts call `bd`/`dolt` from sh), and on Windows a .bat
// launcher makes it visible to Go's exec.LookPath/CreateProcess PATHEXT
// resolution — an extensionless script is invisible there, so the REAL tool on
// the host PATH would silently take over (doctrine T3; same pattern as
// internal/beads installFakeBDOnPath).
func installFakeToolOnPath(t testing.TB, binDir, name, shBody string) string {
	t.Helper()
	impl := filepath.Join(binDir, name)
	if err := os.WriteFile(impl, []byte(shBody), 0o755); err != nil {
		t.Fatalf("WriteFile fake %s: %v", name, err)
	}
	if runtime.GOOS == "windows" {
		bat := fmt.Sprintf("@\"%s\" \"%s\" %%*\r\n", execshim.ShPath(), filepath.ToSlash(impl))
		if err := os.WriteFile(filepath.Join(binDir, name+".bat"), []byte(bat), 0o755); err != nil {
			t.Fatalf("WriteFile fake %s.bat: %v", name, err)
		}
	}
	return impl
}

// denyDirWrites blocks file creation/writes in dir for the remainder of the
// test. Unix uses mode bits (0o555); Windows mode bits are synthetic
// (doctrine P5), so an explicit ACL deny for Everyone (SID S-1-1-0, locale
// independent) on file-create/write is applied via icacls and removed on
// cleanup so TempDir teardown can proceed.
func denyDirWrites(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatalf("chmod dir read-only: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return
	}
	deny := exec.Command("icacls", dir, "/deny", "*S-1-1-0:(WD,AD,W)")
	if out, err := deny.CombinedOutput(); err != nil {
		t.Fatalf("icacls deny writes: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("icacls", dir, "/remove:d", "*S-1-1-0").Run()
	})
}

// prependPathDir moves dir to the FRONT of env's PATH. Call it after
// sanitizedBaseEnv when a test installs a fake that must shadow a Git for Windows
// coreutil, or must win over a real tool of the same name.
//
// sanitizedBaseEnv finishes by calling execshim.EnvWithShellDir, which PREPENDS
// Git's usr/bin whenever that directory is not already on PATH. On a host whose
// ambient PATH already contains usr/bin the test's bin dir stays first and the fake
// wins; on a host without it (the CI runner) usr/bin lands ahead of the test's bin
// dir and the real tool answers instead. The fake is then never consulted and the
// test quietly stops exercising what it claims.
func prependPathDir(env []string, dir string) []string {
	out := append([]string(nil), env...)
	for i, kv := range out {
		if len(kv) >= 5 && strings.EqualFold(kv[:5], "PATH=") {
			out[i] = "PATH=" + dir + string(os.PathListSeparator) + kv[5:]
			return out
		}
	}
	return append(out, "PATH="+dir)
}

// samePathText reports whether two strings name the same path ignoring
// separator flavor. Pack scripts are Tier-2 POSIX sh and join paths with "/"
// ("$dir/.beads" in gc-beads-bd.sh), so a value that round-trips through a
// script arrives mixed ("C:\...\repo/.beads") even when Go passed it as
// filepath.Join form. Everything downstream is Go or a Windows API, both of
// which accept either separator, so the mixed form is correct — only literal
// string equality is wrong (doctrine P4). Use this for assertions on paths that
// crossed a script boundary; keep exact equality for Go-only values.
func samePathText(a, b string) bool {
	return filepath.ToSlash(a) == filepath.ToSlash(b)
}

// shScriptPath renders p for splicing into sh script text: slash-separated
// (sh eats backslashes in unquoted words, so a native Windows path degrades
// into a mangled relative filename) and single-quoted so temp-dir spaces
// survive (doctrine P8). Identity-shaped on Unix paths.
func shScriptPath(p string) string {
	return "'" + filepath.ToSlash(p) + "'"
}

// writeTestScript creates a shell script that exits with the given code.
// If stderrMsg is non-empty, the script writes it to stderr before exiting.
func writeTestScript(t *testing.T, _ string, exitCode int, stderrMsg string) string {
	t.Helper()
	content := "#!/bin/sh\n"
	if stderrMsg != "" {
		content += "echo '" + stderrMsg + "' >&2\n"
	}
	content += "exit " + itoa(exitCode) + "\n"
	return writeNamedTestScript(t, "test-beads.sh", content)
}

func writeNamedTestScript(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, name)
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return script
}

func writeManagedBdTestScript(t *testing.T, content string) string {
	t.Helper()
	return writeNamedTestScript(t, "gc-beads-bd.sh", content)
}

func itoa(n int) string {
	return []string{"0", "1", "2"}[n]
}

func listenOnRandomPort(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

func reserveRandomTCPPort(t *testing.T) int {
	t.Helper()
	ln := listenOnRandomPort(t)
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func startTCPListenerProcess(t *testing.T, port int) *exec.Cmd {
	t.Helper()
	skipSlowCmdGCTest(t, "spawns a TCP listener process to emulate managed dolt; run make test-cmd-gc-process for full coverage")
	cmd := exec.Command("python3", "-c", `
import signal
import socket
import sys
import time
port = int(sys.argv[1])
sock = socket.socket()
sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
sock.bind(("127.0.0.1", port))
sock.listen(5)
def _stop(*_args):
    raise SystemExit(0)
signal.signal(signal.SIGTERM, _stop)
signal.signal(signal.SIGINT, _stop)
while True:
    time.sleep(1)
`, strconv.Itoa(port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start listener process: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return cmd
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("listener process on %d did not become ready", port)
	return nil
}

func writeDoltState(cityPath string, state doltRuntimeState) error {
	stateDir := filepath.Join(cityPath, ".gc", "runtime", "packs", "dolt")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	data := fmt.Sprintf(`{"running":%t,"pid":%d,"port":%d,"data_dir":%q,"started_at":%q}`,
		state.Running, state.PID, state.Port, state.DataDir, state.StartedAt)
	return os.WriteFile(filepath.Join(stateDir, "dolt-state.json"), []byte(data), 0o644)
}

// setTestHome pins the test's home directory on every platform: os.UserHomeDir
// reads HOME on Unix but USERPROFILE on Windows (doctrine T1), so setting only
// HOME leaves Windows tests operating on the real user profile — the launchd
// plist tests were writing under the developer's actual ~/Library/LaunchAgents.
func setTestHome(t testing.TB, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// grantBroadDirAccess makes dir readable/writable by a broad principal so a test
// can prove that production TIGHTENS a directory rather than merely inheriting a
// restrictive parent ACL. Without this, a Windows ACL assertion under t.TempDir()
// passes even when the production hardening is removed (t.TempDir() already
// inherits an owner-only ACL from the user profile's temp dir), which makes the
// assertion a smoke test instead of a guard.
//
// Unix widens the mode bits for the same reason. Returns after applying, and
// restores on cleanup.
func grantBroadDirAccess(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(dir, 0o777); err != nil {
			t.Fatalf("chmod dir world-writable: %v", err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return
	}
	// *S-1-1-0 = Everyone. (OI)(CI) so children inherit the broad grant, which
	// is what would otherwise mask a missing tighten step.
	grant := exec.Command("icacls", dir, "/grant", "*S-1-1-0:(OI)(CI)(F)")
	if out, err := grant.CombinedOutput(); err != nil {
		t.Fatalf("icacls grant Everyone: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("icacls", dir, "/remove:g", "*S-1-1-0").Run()
	})
}
