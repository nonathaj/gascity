package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/execshim"
)

// TestEnsureDoltIdentityErrorMessages exercises the ensure_dolt_identity
// helper from examples/bd/assets/scripts/gc-beads-bd.sh against stub `dolt`
// and `git` binaries on PATH. The bug being guarded against: when a user
// has set ONLY `dolt config --global --add user.name`, the previous
// implementation reported "git user.name not available" and told the user
// to set user.name (which they already had). The corrected helper reports
// the field that is actually missing — user.email.
func TestEnsureDoltIdentityErrorMessages(t *testing.T) {
	t.Parallel()

	bashPath := resolveShellHarnessBash(t)

	root := repoRootForLint(t)
	scriptPath := filepath.Join(root, "examples", "bd", "assets", "scripts", "gc-beads-bd.sh")
	scriptBytes, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	fnSrc := extractShellFunction(t, string(scriptBytes), "ensure_dolt_identity")

	type fakeStore struct {
		name  string
		email string
	}
	type wantOutcome struct {
		exitOK             bool
		mustContain        []string
		mustNotContain     []string
		expectDoltNameSet  string
		expectDoltEmailSet string
	}
	cases := []struct {
		name string
		dolt fakeStore
		git  fakeStore
		want wantOutcome
	}{
		{
			name: "dolt_has_both_returns_ok",
			dolt: fakeStore{name: "Roger", email: "roger@example.com"},
			want: wantOutcome{exitOK: true},
		},
		{
			name: "dolt_only_name_git_empty_reports_email_missing_not_name",
			dolt: fakeStore{name: "Roger"},
			want: wantOutcome{
				exitOK:         false,
				mustContain:    []string{"user.email"},
				mustNotContain: []string{`add user.name "Your Name"`},
			},
		},
		{
			name: "dolt_only_email_git_empty_reports_name_missing_not_email",
			dolt: fakeStore{email: "roger@example.com"},
			want: wantOutcome{
				exitOK:         false,
				mustContain:    []string{"user.name"},
				mustNotContain: []string{`add user.email "you@example.com"`},
			},
		},
		{
			name: "dolt_empty_git_empty_reports_both_missing",
			want: wantOutcome{
				exitOK:      false,
				mustContain: []string{"user.name", "user.email"},
			},
		},
		{
			name: "dolt_empty_git_has_both_backfills_dolt",
			git:  fakeStore{name: "Roger", email: "roger@example.com"},
			want: wantOutcome{
				exitOK:             true,
				expectDoltNameSet:  "Roger",
				expectDoltEmailSet: "roger@example.com",
			},
		},
		{
			name: "dolt_name_git_email_backfills_only_email",
			dolt: fakeStore{name: "Roger"},
			git:  fakeStore{email: "roger@example.com"},
			want: wantOutcome{
				exitOK:             true,
				expectDoltEmailSet: "roger@example.com",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doltLog := filepath.Join(t.TempDir(), "dolt-set.log")

			// Define git/dolt as shell FUNCTIONS prepended to the script rather
			// than fake executables on PATH. Functions unconditionally shadow
			// PATH lookups on every platform, so this is immune to MSYS/git-bash
			// executable-resolution heuristics — a Go-written extensionless
			// "0o755" script is NOT seen as executable by git-bash, so it fell
			// through to the real git.exe (which on the runner has no global
			// user.name → empty → "identity incomplete"). Functions never do.
			script := fakeGitFunc(tc.git.name, tc.git.email) +
				fakeDoltFunc(tc.dolt.name, tc.dolt.email) +
				fnSrc + "\n" +
				"die() { printf '%s\\n' \"$*\" >&2; exit 1; }\n" +
				"ensure_dolt_identity\n"

			cmd := exec.Command(bashPath, "-c", script)
			cmd.Env = envWithOverrides(os.Environ(), map[string]string{
				"FAKE_DOLT_LOG": doltLog,
			})
			var stdout, stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			runErr := cmd.Run()

			if tc.want.exitOK {
				if runErr != nil {
					t.Fatalf("expected success, got %v\nbash=%q\nstdout:\n%s\nstderr:\n%s\ndolt-log:\n%s",
						runErr, bashPath, stdout.String(), stderr.String(), readFile(doltLog))
				}
			} else {
				if runErr == nil {
					t.Fatalf("expected non-zero exit, got success\nstderr:\n%s", stderr.String())
				}
			}
			out := stderr.String()
			for _, frag := range tc.want.mustContain {
				if !strings.Contains(out, frag) {
					t.Errorf("stderr missing %q:\n%s", frag, out)
				}
			}
			for _, frag := range tc.want.mustNotContain {
				if strings.Contains(out, frag) {
					t.Errorf("stderr should not contain %q (it is misleading guidance):\n%s", frag, out)
				}
			}
			if tc.want.expectDoltNameSet != "" {
				if !logContains(doltLog, "set user.name "+tc.want.expectDoltNameSet) {
					t.Errorf("expected dolt user.name to be set to %q; log:\n%s",
						tc.want.expectDoltNameSet, readFile(doltLog))
				}
			}
			if tc.want.expectDoltEmailSet != "" {
				if !logContains(doltLog, "set user.email "+tc.want.expectDoltEmailSet) {
					t.Errorf("expected dolt user.email to be set to %q; log:\n%s",
						tc.want.expectDoltEmailSet, readFile(doltLog))
				}
			}
		})
	}
}

// resolveShellHarnessBash returns a POSIX bash for running the pack's shell
// functions, skipping the test when none is available. On Windows it MUST be
// Git for Windows' bash (next to the sh execshim resolves) — the runner's
// System32\bash.exe is the WSL launcher, which runs in a separate Linux
// filesystem namespace and cannot see the Windows temp-dir fake git/dolt on
// PATH, so the harness would exec the real tools (or fail) instead of the stubs.
func resolveShellHarnessBash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		shDir := filepath.Dir(execshim.ShPath())
		if filepath.IsAbs(shDir) {
			if cand := filepath.Join(shDir, "bash.exe"); fileExists(cand) {
				return cand
			}
		}
		t.Skip("Git for Windows bash not found; skipping shell-function test (System32 bash.exe is WSL and cannot see Windows PATH fakes)")
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	t.Skip("bash not available; skipping shell-function test")
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// envWithOverrides returns base with the given keys set, replacing any existing
// entry for each key CASE-INSENSITIVELY. This matters on Windows where the
// environment is case-insensitive: os.Environ() yields "Path=..." and a naive
// append of "PATH=..." would leave a duplicate that CreateProcess resolves to
// the wrong value.
func envWithOverrides(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, kv := range base {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		skip := false
		for ok := range overrides {
			if strings.EqualFold(key, ok) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}

func extractShellFunction(t *testing.T, script, name string) string {
	t.Helper()
	// Match the function header and capture lines until the matching
	// closing brace at column 0. The script uses the conventional
	// `name() {` ... `\n}` shape.
	pattern := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(name) + `\(\)\s*\{.*?\n\}`)
	loc := pattern.FindStringIndex(script)
	if loc == nil {
		t.Fatalf("could not find shell function %q in script", name)
	}
	return script[loc[0]:loc[1]]
}

// fakeDoltFunc returns a `dolt() { ... }` shell-function definition that stubs
// the `config --global --get|--add user.name|user.email` surface
// ensure_dolt_identity uses, logging `--add`s to $FAKE_DOLT_LOG. Uses return,
// not exit, so it never terminates the enclosing script.
func fakeDoltFunc(name, email string) string {
	return `dolt() {
  log_file=${FAKE_DOLT_LOG:-/dev/null}
  case "$1 $2" in
    "config --global")
      case "$3" in
        --get)
          case "$4" in
            user.name) ` + emitGetIf(name) + ` ;;
            user.email) ` + emitGetIf(email) + ` ;;
          esac
          ;;
        --add)
          echo "set $4 $5" >> "$log_file"
          return 0
          ;;
      esac
      ;;
  esac
  return 0
}
`
}

// fakeGitFunc returns a `git() { ... }` shell-function definition that stubs the
// `config --global user.name|user.email` reads ensure_dolt_identity performs.
func fakeGitFunc(name, email string) string {
	return `git() {
  case "$1 $2" in
    "config --global")
      case "$3" in
        user.name) ` + emitGetIf(name) + ` ;;
        user.email) ` + emitGetIf(email) + ` ;;
      esac
      ;;
  esac
  return 0
}
`
}

func emitGetIf(value string) string {
	if value == "" {
		return "return 1"
	}
	return "echo " + value + "; return 0"
}

// writeExecutable writes a fake tool script via installFakeToolOnPath (which
// also emits a .bat launcher for PATHEXT-resolving invokers). Callers that run
// the fake through git-bash should prefer a shell-function stub instead —
// git-bash does not treat a Go-written extensionless script as executable.
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	installFakeToolOnPath(t, filepath.Dir(path), filepath.Base(path), body)
}

func logContains(path, want string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), want)
}

func readFile(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}
