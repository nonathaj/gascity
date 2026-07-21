//go:build !windows

package exec

import "os/exec"

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
