package testutil

import (
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// pythonCandidates are argv prefixes tried in order.
//
//   - "python3" is the Unix spelling and the only one present on most Linux and
//     macOS hosts.
//   - "python" covers Windows installers, which ship python.exe and generally no
//     python3.exe at all.
//   - "py -3" is the Windows Python Launcher, the canonical way to reach an
//     interpreter there. It is last because it exists only on Windows, but it is
//     often the only candidate that works: both bare names may be shadowed by
//     shims (see PythonCommand).
var pythonCandidates = [][]string{
	{"python3"},
	{"python"},
	{"py", "-3"},
}

var resolvedPython struct {
	sync.Once
	prefix []string
}

// PythonCommand returns an *exec.Cmd running args under a Python interpreter
// that actually executes code, or skips the test when the host has none.
//
// Resolution runs each candidate rather than trusting exec.LookPath. On Windows
// both "python3" and "python" commonly resolve to something that starts cleanly
// and exits 0 having run nothing: a pyenv shim forwards to `pyenv exec <name>`
// for a version that provides only python.exe, and the Microsoft Store stub in
// WindowsApps behaves similarly. A caller that spawned a listener through one of
// those saw Start succeed, no process bind the port, and the test fail its
// readiness deadline -- a failure indistinguishable from a slow machine, which
// is exactly how it was first misdiagnosed. Requiring observed output is what
// separates a working interpreter from a silent no-op.
func PythonCommand(t testing.TB, args ...string) *exec.Cmd {
	t.Helper()
	prefix := pythonPrefix(t)
	full := append(append([]string(nil), prefix[1:]...), args...)
	return exec.Command(prefix[0], full...)
}

func pythonPrefix(t testing.TB) []string {
	t.Helper()
	resolvedPython.Do(func() {
		const marker = "gascity-python-probe"
		for _, candidate := range pythonCandidates {
			path, err := exec.LookPath(candidate[0])
			if err != nil {
				continue
			}
			args := append(append([]string(nil), candidate[1:]...), "-c", "print('"+marker+"')")
			out, err := exec.Command(path, args...).Output()
			if err != nil || !strings.Contains(string(out), marker) {
				continue
			}
			resolvedPython.prefix = append([]string{path}, candidate[1:]...)
			return
		}
	})
	if len(resolvedPython.prefix) == 0 {
		names := make([]string, 0, len(pythonCandidates))
		for _, candidate := range pythonCandidates {
			names = append(names, strings.Join(candidate, " "))
		}
		t.Skipf("no working Python interpreter found (tried %s); "+
			"a name that resolves but produces no output does not count",
			strings.Join(names, ", "))
	}
	return resolvedPython.prefix
}
