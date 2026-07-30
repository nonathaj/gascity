package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// shCommand runs a POSIX sh script the same way the wrappers do. The scripts
// package already invokes "bash" directly for hook and guard scripts; sh is
// available alongside it on every supported platform, including Git for
// Windows.
func shCommand(args ...string) *exec.Cmd {
	return testCommand("sh", args...)
}

// windowsEssentialEnv returns the Windows variables any test that replaces a
// child's environment wholesale must carry, mirroring the Makefile's TEST_ENV
// allowlist and documented in the comment above it. Two matter most here:
// without TMP/TEMP, os.TempDir() -- which reads those on Windows rather than
// TMPDIR -- falls back to C:\WINDOWS and `go test` dies with "creating work
// dir: Access is denied"; without USERPROFILE, Go cannot derive a default
// GOPATH and fails with "module cache not found". Every entry is skipped when
// empty, so this is a no-op on Unix.
func windowsEssentialEnv() []string {
	var env []string
	for _, key := range []string{
		"TMP",
		"TEMP",
		"SYSTEMROOT",
		"SYSTEMDRIVE",
		"WINDIR",
		"COMSPEC",
		"PATHEXT",
		"USERPROFILE",
		"LOCALAPPDATA",
		"APPDATA",
		"PROGRAMDATA",
		"NUMBER_OF_PROCESSORS",
	} {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

// environWithout returns env with every assignment of name removed. Windows
// environment variables are case-insensitive, so the match is too.
func environWithout(env []string, name string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(strings.ToUpper(entry), strings.ToUpper(prefix)) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// resolveDefaultTMPDir runs the shared helper that every test-running wrapper
// (Makefile TEST_ENV, and the shard scripts below) consults for its TMPDIR
// fallback, with TMPDIR unset so the helper picks a default. The helper, not a
// literal in this file, is the single source of truth for the policy:
//
//	Off the shared tmpfs -- on the Linux fleet /tmp is a size-capped RAM-backed
//	tmpfs shared by every executor (see AGENTS.md "Build Cache Conventions"), so
//	the on-disk /var/tmp wins wherever it exists.
//
//	Short -- internal/testutil.ShortTempDir roots test-owned socket directories
//	at os.TempDir(), and Unix socket paths built under it must stay under the
//	sun_path limit (104 bytes on macOS, 108 on Linux and for Windows AF_UNIX;
//	see internal/runtime/acp and internal/runtime/subprocess).
//
// Git for Windows ships no /var at all, so a hardcoded /var/tmp was a
// nonexistent path there; its /tmp is the on-disk user temp rather than a
// tmpfs, so the fallback satisfies both constraints on the one platform that
// reaches it.
func resolveDefaultTMPDir(t *testing.T) string {
	t.Helper()
	repoRoot := repoRoot(t)
	cmd := shCommand(filepath.Join(repoRoot, "scripts", "lib", "default-tmpdir.sh"))
	cmd.Dir = repoRoot
	// Drop TMPDIR rather than setting it empty: MSYS rewrites path-shaped
	// variables when it starts a process, and an explicitly empty TMPDIR came
	// back through that translation as a cwd-derived path, which the helper then
	// echoed as a caller-supplied value.
	cmd.Env = environWithout(os.Environ(), "TMPDIR")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("resolve default tmpdir: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestDefaultTMPDirStaysOffSharedTmpTmpfs guards ga-ntbpyb.4 at its source: the
// resolved default must prefer the on-disk /var/tmp and only reach /tmp on a
// host that has no /var/tmp at all.
func TestDefaultTMPDirStaysOffSharedTmpTmpfs(t *testing.T) {
	got := resolveDefaultTMPDir(t)
	if _, err := os.Stat("/var/tmp"); err == nil {
		if got != "/var/tmp" {
			t.Fatalf("default TMPDIR = %q on a host that has /var/tmp, want %q", got, "/var/tmp")
		}
		return
	}
	if got != "/tmp" {
		t.Fatalf("default TMPDIR = %q on a host without /var/tmp, want %q", got, "/tmp")
	}
	if info, err := os.Stat(got); err != nil || !info.IsDir() {
		t.Fatalf("default TMPDIR %q is not a usable directory: %v", got, err)
	}
}

// TestMakefileTestEnvDefaultsTMPDirToSharedHelper guards ga-ntbpyb.4: make
// test-fast-parallel (and every other $(TEST_ENV)-wrapped target) must take its
// fallback from the shared helper rather than an independent literal, so the
// policy cannot drift between the Makefile and the shard scripts.
func TestMakefileTestEnvDefaultsTMPDirToSharedHelper(t *testing.T) {
	got := runMakefileTestEnvTMPDirPrintTarget(t, nil)
	if want := resolveDefaultTMPDir(t); got != want {
		t.Fatalf("TEST_ENV TMPDIR = %q, want the shared helper's %q", got, want)
	}
}

// TestMakefileTestEnvRespectsCallerSuppliedTMPDir guards the other half of
// the same fallback expression: a caller (CI, a developer's shell, a deploy
// gate) that already exports TMPDIR to somewhere sane must still have that
// value win, not get silently overridden by the new default.
func TestMakefileTestEnvRespectsCallerSuppliedTMPDir(t *testing.T) {
	custom := filepath.Join(t.TempDir(), "caller-tmpdir")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatalf("mkdir custom TMPDIR: %v", err)
	}
	got := runMakefileTestEnvTMPDirPrintTarget(t, []string{"TMPDIR=" + custom})
	// Compare directories, not spellings. t.TempDir() hands back a native
	// Windows path while the shell reports the MSYS translation of the same
	// directory (C:\Users\...\Temp\x becomes /tmp/x), so a string match failed
	// on a value that was in fact the caller-supplied one. Resolving both sides
	// through the shell puts them in one namespace; on Unix it is identity.
	if gotDir, wantDir := canonicalizeViaSh(t, got), canonicalizeViaSh(t, custom); gotDir != wantDir {
		t.Fatalf("TEST_ENV TMPDIR = %q (resolves to %q), want caller-supplied %q (resolves to %q)",
			got, gotDir, custom, wantDir)
	}
}

// canonicalizeViaSh resolves a path to the shell's own canonical form so paths
// produced by Go and by the shell can be compared on Windows, where they name
// the same directory in different namespaces.
// canonicalizeParentViaSh is canonicalizeViaSh for the directory holding path.
// The dirname is taken inside the shell because path may already be in the
// shell's namespace ("/tmp/..."), which Go's filepath would mis-split on
// Windows.
func canonicalizeParentViaSh(t *testing.T, path string) string {
	t.Helper()
	cmd := shCommand("-c", `cd "$(dirname "$1")" && pwd -P`, "sh", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("canonicalize parent of %q via sh: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

func canonicalizeViaSh(t *testing.T, path string) string {
	t.Helper()
	cmd := shCommand("-c", `cd "$1" && pwd -P`, "sh", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("canonicalize %q via sh: %v", path, err)
	}
	return strings.TrimSpace(string(out))
}

// TestMakefileTestEnvTMPDirDefaultLeavesSocketPathHeadroom proves the actual
// resolved default (not just an assumed literal) leaves enough room for a
// realistic Unix socket path. Mirrors the "socks/<hashed-key>.sock" shape
// built by internal/runtime/subprocess.Provider.sockPath and
// internal/runtime/acp.Provider.sockPath: a short prefix directory (per
// internal/testutil.ShortTempDir) holding a "socks" dir and a 9-byte hashed
// key ("s" + 8 hex chars) plus ".sock".
func TestMakefileTestEnvTMPDirDefaultLeavesSocketPathHeadroom(t *testing.T) {
	root := runMakefileTestEnvTMPDirPrintTarget(t, nil)
	shortDir := filepath.Join(root, "gc-t-123456789")
	sockPath := filepath.Join(shortDir, "socks", "s01234567.sock")
	const sunPathLimit = 104 // stricter of macOS(104)/Linux(108) sun_path limits
	const wantHeadroom = 20  // arbitrary but generous safety margin in bytes
	if margin := sunPathLimit - len(sockPath); margin < wantHeadroom {
		t.Fatalf("socket path %q (%d bytes) leaves only %d bytes of headroom under the sun_path limit %d; want >= %d",
			sockPath, len(sockPath), margin, sunPathLimit, wantHeadroom)
	}
}

func runMakefileTestEnvTMPDirPrintTarget(t *testing.T, extraEnv []string) string {
	t.Helper()
	repoRoot := repoRoot(t)
	makefile, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	tmp := t.TempDir()
	testMakefile := filepath.Join(tmp, "Makefile")
	content := string(makefile) + `
.PHONY: print-test-env-tmpdir
print-test-env-tmpdir:
	@$(TEST_ENV) sh -c 'echo TMPDIR=$$TMPDIR'
`
	if err := os.WriteFile(testMakefile, []byte(content), 0o644); err != nil {
		t.Fatalf("write test Makefile: %v", err)
	}

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"USER=" + os.Getenv("USER"),
		"SHELL=/bin/sh",
	}
	env = append(env, extraEnv...)

	cmd := makeCommand("--no-print-directory", "-f", testMakefile, "print-test-env-tmpdir")
	cmd.Dir = repoRoot
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make print-test-env-tmpdir failed: %v\n%s", err, out)
	}
	line := strings.TrimSpace(string(out))
	const prefix = "TMPDIR="
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("unexpected output from print-test-env-tmpdir: %q", line)
	}
	return strings.TrimPrefix(line, prefix)
}

// tmpdirHelperReferences documents every wrapper that builds its own env -i
// around go test (or mktemps its own scratch) and must therefore resolve its
// TMPDIR fallback through the shared helper. Each count is the exact number of
// helper references in that file today; a changed count means a site was added
// or removed and this ledger must be updated deliberately, not silently.
var tmpdirHelperReferences = map[string]int{
	"scripts/test-local-parallel":    2, // log_dir mktemp + per-job env
	"scripts/go-test-observable":     1, // per-run log file mktemp
	"scripts/test-go-test-shard":     1, // per-shard env, resolved once
	"scripts/test-integration-shard": 1, // per-shard env
	"Makefile":                       1, // TMPDIR_DEFAULT for TEST_ENV
}

// TestWrappersResolveTMPDirThroughSharedHelper is the sibling-targets half of
// ga-ntbpyb.4: test-cmd-gc-process-parallel, test-integration-shards-parallel,
// and test-local-full-parallel all fan out through these scripts directly (not
// through the Makefile's TEST_ENV), so each must independently route to the
// helper. Hardcoded literals are rejected outright: "/tmp" put scratch on the
// shared tmpfs, and "/var/tmp" was a nonexistent path on Git for Windows.
func TestWrappersResolveTMPDirThroughSharedHelper(t *testing.T) {
	repoRoot := repoRoot(t)
	const helperRef = "default-tmpdir.sh"
	bannedLiterals := []string{"${TMPDIR:-/tmp}", "${TMPDIR:-/var/tmp}", "$${TMPDIR:-/tmp}", "$${TMPDIR:-/var/tmp}"}
	for relPath, wantCount := range tmpdirHelperReferences {
		t.Run(relPath, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(repoRoot, relPath))
			if err != nil {
				t.Fatalf("read %s: %v", relPath, err)
			}
			content := string(data)
			for _, banned := range bannedLiterals {
				if strings.Contains(content, banned) {
					t.Fatalf("%s hardcodes the TMPDIR fallback via %q instead of the shared helper", relPath, banned)
				}
			}
			if got := strings.Count(content, helperRef); got != wantCount {
				t.Fatalf("%s has %d references to %q, want %d", relPath, got, helperRef, wantCount)
			}
		})
	}
}
