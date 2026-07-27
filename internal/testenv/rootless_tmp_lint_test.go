package testenv_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// rootlessTmpAllowMarker opts a single line out of TestTestsDoNotAssumeARootlessTmp.
//
// The opt-out is a marker at the use site rather than a central allowlist on purpose: a
// list in this file has to be edited by someone who is not looking at the code being
// exempted, and it goes stale silently when that code moves. A marker travels with the
// line and states its reason where the next reader is already looking.
const rootlessTmpAllowMarker = "gc:allow-rootless-tmp"

// pathCreatingFuncs are the calls that make a rootless "/tmp" argument actually break.
//
// Scope matters here. A plain sweep for "/tmp" literals in test files reports 1118 hits,
// and nearly all of them are inert fixture strings — a "/tmp/city" handed to config so an
// assertion has a path-shaped value to compare, never touching the filesystem. Those are a
// different class (rootless absolute fixtures, doctrine T2, fixed with testutil.AbsFixture)
// and mostly harmless. A lint that reports a thousand findings gets switched off, or forces
// a mass rewrite whose risk exceeds the bug's.
//
// What broke three times was narrower and specific: "/tmp" used as a root that gets
// CREATED or BOUND, where Windows has no C:\tmp to create in. That is what this checks.
// The package qualifier is part of the key, not just the function name. Matching bare names
// flagged beadstest.Create(store, map[string]any{"work_dir": "/tmp/myrig"}) — a bead whose
// work_dir is inert fixture text, nowhere near the filesystem. "Create" is too common a
// method name to match unqualified.
var pathCreatingFuncs = map[string]bool{
	"os.Mkdir":                    true,
	"os.MkdirAll":                 true,
	"os.MkdirTemp":                true,
	"os.Create":                   true,
	"os.CreateTemp":               true,
	"os.OpenFile":                 true,
	"os.WriteFile":                true,
	"os.Symlink":                  true,
	"net.Listen":                  true,
	"net.ListenUnix":              true,
	"net.Dial":                    true,
	"net.DialUnix":                true,
	"net.ResolveUnixAddr":         true,
	"tmuxtest.NewSocketParentDir": true,
}

// pathAssemblyFuncs build a path from parts, so a "/tmp" literal inside one still lands in
// the enclosing call's argument. Descent is limited to these: walking every nested node
// reached into composite literals and reported their inert values.
var pathAssemblyFuncs = map[string]bool{
	"filepath.Join": true,
	"path.Join":     true,
	"fmt.Sprintf":   true,
}

// TestTestsDoNotAssumeARootlessTmp fails when a test file uses a rootless "/tmp" path.
//
// "/tmp" has no volume on Windows, so it is NOT an absolute path there: it resolves
// against the current drive. A test that passes it as a directory root therefore works on
// any dev box that happens to have a D:\tmp and fails only on CI, which has no C:\tmp.
//
// This lint exists because the class recurred three times, and each time it was invisible
// locally (doctrine T12):
//
//  1. TestRemoveStaleManagedDoltSocketsWithoutLsofKeepsSocket — AF_UNIX bind under /tmp.
//  2. createAgedFreeTmuxSocketParent — os.MkdirTemp("/tmp", ...).
//  3. TestCmdGCTmuxSocketRootUsesShortPath — "/tmp" as a socket parent root, where
//     NewSocketParentDir then failed and the caller silently took a fallback branch that
//     returned a nil sentinel. The test still passed, asserting nothing.
//
// Case 3 is why prose in a doctrine file was not enough. The failure was not merely
// platform-specific, it was SILENT: a degraded fallback rather than an error. Only a
// build-time check turns that back into something a developer sees before pushing.
//
// Use testutil.ShortTempDir (or a helper like shortSocketRoot that returns /tmp on Unix
// and a short LOCALAPPDATA path on Windows) instead.
func TestTestsDoNotAssumeARootlessTmp(t *testing.T) {
	root := repoRoot(t)
	var violations []string

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipRepoLintDir(d.Name()) || (path != root && isNestedWorktreeRoot(path)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, path)
		violations = append(violations, scanRootlessTmp(filepath.ToSlash(rel), src)...)
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk repo: %v", walkErr)
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("test files use a rootless \"/tmp\" path, which is not absolute on Windows "+
			"and resolves against the current drive — green on a dev box with a D:\\tmp, "+
			"failing on CI which has no C:\\tmp:\n  %s\n\n"+
			"Use testutil.ShortTempDir(t, prefix), or a helper that returns /tmp on Unix and a "+
			"short LOCALAPPDATA path on Windows. If the path is genuinely Unix-only (a sweep "+
			"that globs /tmp by design, or a test already skipped off Unix), mark the line:\n"+
			"    someCall(\"/tmp\") // %s: <why this is Unix-only>",
			strings.Join(violations, "\n  "), rootlessTmpAllowMarker)
	}
}

// scanRootlessTmp reports rootless-/tmp violations in one file's source, as
// "name:line: call(...)" strings. Split out from the repo walk so the detection rules are
// directly testable: once the tree is clean the walk passes no matter what the rules do,
// and a silently-broken detector would look exactly like a clean repo.
func scanRootlessTmp(name string, src []byte) []string {
	fset := token.NewFileSet()
	// Comments are deliberately not inspected for the /tmp match itself: a comment
	// mentioning /tmp is harmless, and matching raw source text rather than the AST is what
	// makes lints like this noisy enough that someone switches them off.
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		// A test file that does not parse is a different failure, reported elsewhere.
		return nil
	}
	lines := strings.Split(string(src), "\n")
	var violations []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isPathCreatingCall(call) {
			return true
		}
		for _, arg := range call.Args {
			lit := rootlessTmpLiteralIn(arg)
			if lit == nil {
				continue
			}
			line := fset.Position(lit.Pos()).Line
			if lineHasRootlessTmpAllowMarker(lines, line) {
				continue
			}
			violations = append(violations,
				name+":"+strconv.Itoa(line)+": "+callName(call)+"("+lit.Value+" ...)")
		}
		return true
	})
	return violations
}

// TestRootlessTmpDetectionCatchesTheKnownShapes pins the detector against the shapes that
// actually occurred, so the lint cannot quietly decay into one that reports nothing.
//
// Both directions matter. The negative cases are not padding: an over-broad version of this
// rule reported 1118 findings across the repo, nearly all of them inert fixture strings, and
// a lint that cries wolf that loudly is one someone deletes.
func TestRootlessTmpDetectionCatchesTheKnownShapes(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		want bool
	}{{
		name: "MkdirTemp with a rootless tmp root",
		code: `_, _ = os.MkdirTemp("/tmp", "hdr")`,
		want: true,
	}, {
		name: "path assembled by filepath.Join",
		code: `_, _ = os.MkdirTemp(filepath.Join("/tmp", "gct"), "hdr")`,
		want: true,
	}, {
		name: "socket bound under a rootless tmp",
		code: `_, _ = net.ResolveUnixAddr("unix", "/tmp/herdr.sock")`,
		want: true,
	}, {
		name: "socket parent root",
		code: `_, _, _ = tmuxtest.NewSocketParentDir("/tmp", io.Discard)`,
		want: true,
	}, {
		// The false positive that made the first version of this lint unusable.
		name: "inert fixture value inside a composite literal",
		code: `_ = beadstest.Create(store, map[string]any{"work_dir": "/tmp/myrig"})`,
		want: false,
	}, {
		name: "marker on the same line",
		code: `_, _ = os.MkdirTemp("/tmp", "x") // gc:allow-rootless-tmp: unix-only sweep`,
		want: false,
	}, {
		name: "marker in the comment block above",
		code: "// gc:allow-rootless-tmp: the sweep under test globs a hardcoded /tmp,\n" +
			"\t// so the path root is the subject of the test.\n" +
			"\t_, _ = os.MkdirTemp(\"/tmp\", \"x\")",
		want: false,
	}, {
		name: "the recommended replacement",
		code: `dir := testutil.ShortTempDir(t, "hdr")`,
		want: false,
	}, {
		name: "os.TempDir as the root",
		code: `_, _ = os.MkdirTemp(os.TempDir(), "hdr")`,
		want: false,
	}, {
		name: "tmp path that is not rootless",
		code: `_, _ = os.MkdirTemp("/var/tmp", "hdr")`,
		want: false,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\n\nfunc f() {\n\t" + tc.code + "\n}\n"
			got := scanRootlessTmp("synthetic_test.go", []byte(src))
			if (len(got) > 0) != tc.want {
				t.Fatalf("detected=%v want=%v for:\n%s\nfindings: %v",
					len(got) > 0, tc.want, src, got)
			}
		})
	}
}

// callName returns the call's "pkg.Func" name, or "" when it is not a qualified call.
func callName(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

// isPathCreatingCall reports whether call creates or binds a filesystem path.
func isPathCreatingCall(call *ast.CallExpr) bool {
	return pathCreatingFuncs[callName(call)]
}

// rootlessTmpLiteralIn returns a rootless "/tmp" literal passed as expr, either directly or
// assembled by a path-building helper. It deliberately does NOT descend into arbitrary
// expressions: doing so reported map values and struct fields that never reach the
// filesystem.
func rootlessTmpLiteralIn(expr ast.Expr) *ast.BasicLit {
	switch node := expr.(type) {
	case *ast.BasicLit:
		if node.Kind != token.STRING {
			return nil
		}
		value, err := strconv.Unquote(node.Value)
		if err != nil {
			return nil
		}
		if value == "/tmp" || strings.HasPrefix(value, "/tmp/") {
			return node
		}
	case *ast.CallExpr:
		if !pathAssemblyFuncs[callName(node)] {
			return nil
		}
		for _, arg := range node.Args {
			if lit := rootlessTmpLiteralIn(arg); lit != nil {
				return lit
			}
		}
	}
	return nil
}

// lineHasRootlessTmpAllowMarker reports whether the marker appears on the literal's own
// line or anywhere in the contiguous comment block directly above it.
//
// The whole block is searched, not just the line immediately above, because a reason worth
// writing often needs more than one line — and a guard that rejects a two-line
// justification teaches people to delete the explanation rather than write it.
func lineHasRootlessTmpAllowMarker(lines []string, line int) bool {
	if line < 1 || line > len(lines) {
		return false
	}
	if strings.Contains(lines[line-1], rootlessTmpAllowMarker) {
		return true
	}
	for candidate := line - 1; candidate >= 1; candidate-- {
		text := strings.TrimSpace(lines[candidate-1])
		if !strings.HasPrefix(text, "//") {
			return false
		}
		if strings.Contains(text, rootlessTmpAllowMarker) {
			return true
		}
	}
	return false
}
