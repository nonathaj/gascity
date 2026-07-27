package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/execshim"
	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/testutil"
)

// TestProcessArgsReadsAnotherProcessArgvOnEveryPlatform pins the evidence that managed-dolt
// ownership inspection depends on.
//
// managedDoltProcessOwnedWithStateDir decides whether a pid is OUR dolt by looking at its
// argv: does it name our --config, someone else's, or our --data-dir. processArgs is where
// that argv comes from, and it had two sources, neither of which works on Windows:
//
//   - /proc/<pid>/cmdline — there is no /proc
//   - `ps -p <pid> -o args=` — Git for Windows' ps rejects -o outright (the same limitation
//     that caused gw-1ay in the pack script)
//
// So on Windows every ownership question answered "not ours", and the only reason gc still
// recognised its own server was a pid-equality shortcut in managedDoltRuntimeProcessOwned.
// That shortcut is what section 8a of engdocs/contributors/windows-pid-space.md shows must
// go, and it cannot go until this works.
//
// Windows can answer natively: pidutil.Cmdline walks the target process's PEB. This test is
// the contract, and it is deliberately platform-neutral — the same assertion is meaningful
// wherever gc runs, and passes on Linux through /proc.
func TestProcessArgsReadsAnotherProcessArgvOnEveryPlatform(t *testing.T) {
	// A distinctive argument that cannot appear by coincidence, in the shape ownership
	// inspection actually looks for.
	const marker = "--config=/gc-process-args-probe/dolt-config.yaml"
	cmd := exec.Command(execshim.ShPath(), "-c", "sleep 30", marker)
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sh: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = pidutil.KillTree(pid)
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	go func() { _ = cmd.Wait() }()

	if !testutil.WaitUntil(10*time.Second, func() bool { return pidutil.Alive(pid) }) {
		t.Fatalf("probe process %d never became live", pid)
	}

	var got string
	found := testutil.WaitUntil(10*time.Second, func() bool {
		args, err := processArgs(pid)
		if err != nil || args == "" {
			return false
		}
		got = args
		return strings.Contains(args, marker)
	})
	if !found {
		t.Fatalf("processArgs(%d) = %q, want it to contain %q.\n"+
			"Ownership inspection reads argv to tell OUR dolt from an imposter, so a platform "+
			"where argv is unreadable answers \"not ours\" for every pid — which is why the "+
			"pid-equality shortcut in managedDoltRuntimeProcessOwned cannot be removed until "+
			"this passes. Windows can answer natively via pidutil.Cmdline (PEB walk); it must "+
			"not depend on /proc or on `ps -o`.", pid, got, marker)
	}
}
