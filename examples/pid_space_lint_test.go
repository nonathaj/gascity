package examples_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// interpreterLocalPidMarker opts a pid capture out of the conversion requirement. Put
// it on the capture line or the line above, and say why the pid never leaves sh.
const interpreterLocalPidMarker = "interpreter-local pid"

// shellPidCapture matches a shell pid being captured into a variable: `x=$!` or `x=$$`,
// with or without `local`.
var shellPidCapture = regexp.MustCompile(`(?:^|\s)(?:local\s+)?([A-Za-z_][A-Za-z0-9_]*)=\$(?:!|\$)(?:\s|$|;)`)

// TestPackScriptsDeclareThePidSpaceTheyCapture enforces the process-identity boundary
// contract for pack scripts (engdocs/contributors/windows-pid-space.md).
//
// WHY THIS IS A LINT AND NOT A DOC LINE: under Git for Windows `$!` and `$$` are MSYS
// pids, a different numbering space from the native pids every Go-side probe uses
// (OpenProcess, taskkill). A captured pid that reaches a file Go reads must be
// converted with native_pid_of first. When that was missed, gc mis-detected the managed
// dolt server and `gc dolt stop` could terminate an unrelated process holding the same
// number (gw-dbm).
//
// Doctrine T12 already documented an analogous Windows rule in prose and that class
// still recurred three times in one month. Prose does not hold; a check does. Every
// Windows class that has actually stuck in this repo is backed by a helper you must
// call or a test that fails — execshim (P1/P2), winsec (P5), pidutil (P7), winjob (P9).
//
// The rule is intentionally about the CAPTURE SITE, not dataflow: tracking a variable
// to a `> "$PID_FILE"` or `save_state` is a dataflow problem a regex cannot do
// honestly. Instead every capture must declare its intent — convert it, or mark it
// interpreter-local. That is cheap to satisfy and impossible to satisfy by accident.
func TestPackScriptsDeclareThePidSpaceTheyCapture(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filename)

	var violations []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".sh") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			code := line
			if idx := strings.Index(code, "#"); idx >= 0 {
				code = code[:idx]
			}
			m := shellPidCapture.FindStringSubmatch(code)
			if m == nil {
				continue
			}
			// Declared interpreter-local on this line or the one above.
			context := line
			if i > 0 {
				context = lines[i-1] + "\n" + line
			}
			if strings.Contains(context, interpreterLocalPidMarker) {
				continue
			}
			// Or converted near the capture. The window spans a normal explanatory
			// comment block, but not a whole function body.
			converted := false
			for j := i; j < len(lines) && j <= i+12; j++ {
				if strings.Contains(lines[j], "native_pid_of") {
					converted = true
					break
				}
			}
			if converted {
				continue
			}
			violations = append(violations, fmt.Sprintf(
				"%s:%d: %q captures a shell pid (%s) without converting it or declaring it local",
				rel, i+1, m[1], strings.TrimSpace(code)))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking pack scripts: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("pack scripts must declare the pid space they capture — either convert "+
			"with native_pid_of before the value can reach a file Go reads, or add the "+
			"comment %q saying why it never leaves sh (see "+
			"engdocs/contributors/windows-pid-space.md):\n  %s",
			interpreterLocalPidMarker, strings.Join(violations, "\n  "))
	}
}
