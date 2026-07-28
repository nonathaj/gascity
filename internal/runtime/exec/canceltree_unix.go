//go:build !windows

package exec

import (
	"os/exec"
	"sync/atomic"

	"github.com/gastownhall/gascity/internal/execgrace"
)

// cancelKillTree returns a cmd.Cancel matching exec's default (kill the
// process). On Unix the provider script runs directly (no sh wrapper),
// so there is no orphaned grandchild to sweep (gw-ho3 is Windows-only);
// WaitDelay closes any pipe a backgrounded grandchild still holds.
func cancelKillTree(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
}

// cancelAdapter is the cmd.Cancel the adapter runner installs. On Unix the
// adapter runs in its own process group, so cancellation is COOPERATIVE:
// interrupt the group first (letting an INT trap run its rollback) and only
// escalate to Kill if that fails. Composing here — rather than assigning
// cmd.Cancel twice — is what keeps the cooperative path from being silently
// overwritten by the tree-kill fallback.
// Delegates to execgrace.InterruptThenKill, which upstream extracted from this
// package's own former interruptThenKill. Same behavior, one implementation.
func cancelAdapter(cmd *exec.Cmd, accepted *atomic.Bool) func() error {
	return execgrace.InterruptThenKill(cmd, accepted)
}
