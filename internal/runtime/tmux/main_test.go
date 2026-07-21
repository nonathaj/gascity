package tmux

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/gastownhall/gascity/internal/testutil"
	"github.com/gastownhall/gascity/test/tmuxtest"
)

// TestMain neutralizes ambient tmux/session state for the whole package.
// GC_AGENT_SLICE activates real pane-command wrapping inside any test process
// on hosts that export it, which would break exact-argv and pane-command
// assertions across both the unit and integration tiers. Tests that exercise
// wrapping opt back in per-test with t.Setenv. This file is untagged so the
// neutralization applies to every build of the package.
func TestMain(m *testing.M) {
	// Hard lifetime bound: this package spawns real tmux/psmux servers; a
	// killed `go test` run orphans the binary on Windows (incident gw-qhs).
	testutil.StartExitWatchdog()

	_ = os.Unsetenv(AgentSliceEnv)

	// /tmp keeps macOS socket paths under the 104-byte AF_UNIX limit;
	// Windows has no /tmp (psmux uses named pipes, no length concern),
	// so fall back to the default temp root there.
	socketParentRoot := "/tmp"
	if runtime.GOOS == "windows" {
		socketParentRoot = ""
	}
	tmuxSocketParent, err := os.MkdirTemp(socketParentRoot, "gct-")
	if err != nil {
		panic("tmux tests: creating socket parent: " + err.Error())
	}
	tmuxSocketRoot := filepath.Join(tmuxSocketParent, "tmux")
	if err := tmuxtest.ConfigureProcessEnv(tmuxSocketRoot); err != nil {
		_ = os.RemoveAll(tmuxSocketParent)
		panic("tmux tests: configuring tmux test env: " + err.Error())
	}

	if _, err := exec.LookPath("tmux"); err == nil {
		tmuxtest.KillAllTestSessions(mainTB{})
	}
	code := m.Run()
	if _, err := exec.LookPath("tmux"); err == nil {
		tmuxtest.KillAllTestSessions(mainTB{})
	}
	_ = os.RemoveAll(tmuxSocketParent)
	os.Exit(code)
}

type mainTB struct{ testing.TB }

func (mainTB) Helper()             {}
func (mainTB) Logf(string, ...any) {}
