// Package winportlint enforces the Windows-portability doctrine
// (engdocs/contributors/windows-portability.md) with policy tests, the
// same mechanism the repo uses for its other invariants (the testenv
// import lint, the worker-boundary import test, the OpenAPI sync gate).
// A class that has already bitten us at incident scale must not be
// reintroducible by a green PR on a Linux dev box.
package winportlint

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// greenListedDirs returns the set of slash-form repo-relative package
// directories in the Windows green list — the packages the doctrine
// binds. Reads .github/windows-test-packages.txt (the same file the
// gate consumes).
func greenListedDirs(t *testing.T, root string) map[string]bool {
	t.Helper()
	f, err := os.Open(filepath.Join(root, ".github", "windows-test-packages.txt"))
	if err != nil {
		t.Fatalf("open green list: %v", err)
	}
	defer f.Close() //nolint:errcheck // read-only
	dirs := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		dirs[strings.TrimPrefix(line, "./")] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan green list: %v", err)
	}
	return dirs
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	dir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find repo root")
		}
		dir = parent
	}
}

// goFile is one visited source file.
type goFile struct {
	rel     string // slash-form repo-relative path
	base    string
	isTest  bool
	content string
}

// walkGoFiles visits every .go file under the repo, skipping vendored
// and generated trees.
func walkGoFiles(t *testing.T, root string, visit func(goFile)) {
	t.Helper()
	skipDir := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		// Generated client/spec surfaces are not hand-maintained.
		"genclient": true, "generated": true,
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		visit(goFile{
			rel:     filepath.ToSlash(rel),
			base:    d.Name(),
			isTest:  strings.HasSuffix(d.Name(), "_test.go"),
			content: string(data),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

// TestNoBareTestBinarySuffixChecks enforces doctrine P6: a bare
// `".test"` suffix/contains check misses Windows' ".test.exe" and
// fork-bombed the host (gw-8g5). The only sanctioned test-binary
// detector is execshim.IsGoTestExecutable. Repo-wide and line-precise
// (a HasSuffix elsewhere in a file plus a ".test" in a comment is not
// a violation): the flagged line must itself string-check ".test".
func TestNoBareTestBinarySuffixChecks(t *testing.T) {
	root := repoRoot(t)
	// Files that legitimately implement or narrowly wrap the detector.
	exempt := map[string]bool{
		"internal/execshim/execshim.go": true, // canonical implementation
		"internal/testenv/testenv.go":   true, // documented import-cycle copy
	}
	var violations []string
	walkGoFiles(t, root, func(f goFile) {
		if f.isTest || exempt[f.rel] {
			return
		}
		for i, line := range strings.Split(f.content, "\n") {
			if !strings.Contains(line, `".test"`) {
				continue
			}
			if strings.Contains(line, "HasSuffix") || strings.Contains(line, "Contains") {
				violations = append(violations, fmt.Sprintf("%s:%d: bare \".test\" string check", f.rel, i+1))
			}
		}
	})
	if len(violations) > 0 {
		t.Fatalf("bare Go-test-binary string checks miss \".test.exe\" on Windows (doctrine P6); use execshim.IsGoTestExecutable:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// TestNoRawExecLookPathInGreenPackages enforces doctrine P1 for
// production code in green-listed packages: exec.LookPath rejects
// extensionless scripts and misses the Git-for-Windows coreutils
// fallback, so a raw call can pass tests yet fail in Windows
// production (a class the gate does not always catch — the tool may
// resolve another way in CI). The sanctioned resolver is
// execshim.LookPath / execshim.ResolveExecutable.
//
// Scoped to green-listed packages: those have made the Windows-parity
// commitment; the untriaged remainder (chiefly cmd/gc, swept in
// Tranche A) joins as the green list grows. The two allowlisted sites
// resolve the host's own systemd binary on Linux by design.
//
// The doctrine's other test-fixture classes (HOME/USERPROFILE, mode
// bits, path form) are NOT static-linted: they cannot be distinguished
// from legitimate uses without running the code, and the runner gate
// is their precise enforcement. Only unambiguous production classes
// are gated here.
func TestNoRawExecLookPathInGreenPackages(t *testing.T) {
	root := repoRoot(t)
	green := greenListedDirs(t, root)
	allow := map[string]bool{
		// The execshim implementation itself layers the fallback on top
		// of exec.LookPath.
		"internal/execshim/execshim.go": true,
		// Deliberately probes host PATH for the Linux-only systemd-run;
		// "absent on Windows" is the correct answer and the coreutils
		// fallback is meaningless for a non-coreutil.
		"internal/runtime/tmux/agent_slice.go": true,
		// Resolves the host's own systemctl; Linux-only, panics in
		// tests if it resolves into a host dir (guardDelegatedSystemctlPath).
		"cmd/gc/supervisor_systemd_delegate.go": true,
	}
	var violations []string
	walkGoFiles(t, root, func(f goFile) {
		if f.isTest || !green[path.Dir(f.rel)] || allow[f.rel] {
			return
		}
		for i, line := range strings.Split(f.content, "\n") {
			if strings.Contains(line, "exec.LookPath(") {
				violations = append(violations, fmt.Sprintf("%s:%d: raw exec.LookPath", f.rel, i+1))
			}
		}
	})
	if len(violations) > 0 {
		t.Fatalf("green-listed production code uses raw exec.LookPath (doctrine P1); use execshim.LookPath / ResolveExecutable:\n  %s",
			strings.Join(violations, "\n  "))
	}
}
