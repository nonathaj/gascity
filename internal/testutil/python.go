package testutil

import (
	"fmt"
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
// Resolution runs each candidate rather than trusting exec.LookPath, and then
// uses the sys.executable it reports rather than the name that found it. Both
// halves are load-bearing on Windows.
//
// Running the candidate is what separates a working interpreter from a silent
// no-op: "python3" commonly resolves to a pyenv shim that forwards to `pyenv
// exec python3` for a version providing only python.exe, and the Store stub in
// WindowsApps behaves similarly. Either starts cleanly and exits 0 having run
// nothing, so a caller that spawned a listener through one saw Start succeed, no
// process bind the port, and the test fail its readiness deadline -- a failure
// indistinguishable from a slow machine, which is how it was first misdiagnosed.
//
// Using sys.executable is what makes the returned command's PID meaningful.
// Every Windows entry point measured here -- the pyenv shim and the py launcher
// alike -- runs Python in a CHILD process, so cmd.Process.Pid identifies the
// launcher, not the interpreter. Callers that spawn a listener and then assert
// the recorded PID owns the port (managed-dolt runtime-state validation does
// exactly this) compare two different processes and fail. Spawning the reported
// executable directly removes the intermediary.
func PythonCommand(t testing.TB, args ...string) *exec.Cmd {
	t.Helper()
	prefix, err := PythonArgv()
	if err != nil {
		t.Skip(err.Error())
	}
	full := append(append([]string(nil), prefix[1:]...), args...)
	return exec.Command(prefix[0], full...)
}

// PythonArgv is PythonCommand's resolution step for callers that have no
// *testing.T to skip -- helpers that return an error instead. It reports the
// argv prefix of a working interpreter, or an error naming what was tried.
func PythonArgv() ([]string, error) {
	resolvePython()
	if len(resolvedPython.prefix) == 0 {
		names := make([]string, 0, len(pythonCandidates))
		for _, candidate := range pythonCandidates {
			names = append(names, strings.Join(candidate, " "))
		}
		return nil, fmt.Errorf("no working Python interpreter found (tried %s); "+
			"a name that resolves but produces no output does not count",
			strings.Join(names, ", "))
	}
	return append([]string(nil), resolvedPython.prefix...), nil
}

func resolvePython() {
	resolvedPython.Do(func() {
		const marker = "gascity-python-probe"
		for _, candidate := range pythonCandidates {
			path, err := exec.LookPath(candidate[0])
			if err != nil {
				continue
			}
			// One probe answers both questions: whether this candidate executes
			// anything at all, and where the real interpreter lives.
			args := append(append([]string(nil), candidate[1:]...),
				"-c", "import sys; print('"+marker+"', sys.executable)")
			out, err := exec.Command(path, args...).Output()
			if err != nil {
				continue
			}
			executable, ok := strings.CutPrefix(strings.TrimSpace(string(out)), marker+" ")
			if !ok || executable == "" {
				continue
			}
			resolvedPython.prefix = []string{executable}
			return
		}
	})
}
