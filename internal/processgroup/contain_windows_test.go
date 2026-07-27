//go:build windows

package processgroup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/testutil"
)

// spawnOrphanedGrandchild starts `sh -c 'sleep … & echo $!'` and waits for the outer shell to
// exit, leaving the sleep orphaned. It returns the containment attached to the (now dead)
// shell and the grandchild's native pid.
//
// This is the shape that broke: the worker is a GRANDCHILD, and its parent is gone by the time
// anything tries to clean up.
func spawnOrphanedGrandchild(t *testing.T, seconds int) (*Containment, int) {
	t.Helper()
	pidPath := filepath.Join(t.TempDir(), "grandchild.pid")
	script := fmt.Sprintf("sleep %d >/dev/null 2>&1 & echo $! > %q", seconds, filepath.ToSlash(pidPath))
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start shell: %v", err)
	}
	containment := Contain(cmd)
	// The outer shell exits immediately after backgrounding, which is the whole point.
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for shell: %v", err)
	}

	var nativePID int
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, err := os.ReadFile(pidPath)
		data := strings.TrimSpace(string(raw))
		if err == nil && data != "" {
			shellPID, convErr := strconv.Atoi(data)
			if convErr == nil {
				if native, state := testutil.InspectShellPID(shellPID); state == testutil.ShellPIDLive {
					nativePID = native
					break
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild never became observable; pid file contents %q", data)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(func() { _ = pidutil.KillTree(nativePID) })

	if !pidutil.Alive(nativePID) {
		t.Fatalf("grandchild %d not alive at setup, so this test cannot prove anything", nativePID)
	}
	return containment, nativePID
}

// TestContainTerminateReachesAnOrphanedGrandchild is the regression test for the leak.
//
// taskkill /T cannot do this: it walks live parent links, and the grandchild's parent has
// already exited, so the grandchild is nobody's descendant. Job Object membership is
// permanent, so terminating the job still reaches it. If this fails, order execs and
// condition checks are leaking processes again.
func TestContainTerminateReachesAnOrphanedGrandchild(t *testing.T) {
	containment, nativePID := spawnOrphanedGrandchild(t, 45)

	if err := containment.Terminate(); err != nil {
		t.Fatalf("Terminate() error = %v, want nil", err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for pidutil.Alive(nativePID) {
		if time.Now().After(deadline) {
			t.Fatalf("orphaned grandchild %d survived Terminate; the containment is not "+
				"reaching descendants whose parent already exited", nativePID)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestContainReleaseDoesNotKillMembers pins the deliberate choice that makes Release safe to
// call on the success path.
//
// The job is created WITHOUT kill-on-close precisely so that a command which backgrounds a
// daemon on purpose — a pack's start op launching a server through order exec — still has that
// daemon running after a successful run. "Tidying" the job to KillOnClose would silently break
// that, and without this test every other test would still pass while production started
// killing servers the moment it finished starting them.
func TestContainReleaseDoesNotKillMembers(t *testing.T) {
	containment, nativePID := spawnOrphanedGrandchild(t, 30)

	containment.Release()

	// Give a kill-on-close job time to do its damage before concluding it did not.
	time.Sleep(1 * time.Second)
	if !pidutil.Alive(nativePID) {
		t.Fatalf("backgrounded process %d died when the containment was merely RELEASED. "+
			"Release must drop the handle without killing: commands that background a daemon "+
			"on purpose would otherwise lose it on every successful run", nativePID)
	}
}
