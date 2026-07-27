package processgroup

import (
	"os/exec"
	"testing"
)

// TestContainTolboleratesEveryDegenerateInput is a compile-and-call guard for the containment
// API on every platform, including the Unix build where it is inert.
//
// It exists because the Unix implementation is a no-op, and no-ops are exactly what nobody
// tests until one of them panics. Every one of these calls happens for real: Contain runs
// against a command whose Start failed, Release runs twice when cleanup and a deferred release
// both fire, and Terminate runs after Release when a cancellation races a normal return.
func TestContainToleratesEveryDegenerateInput(t *testing.T) {
	t.Run("nil command", func(t *testing.T) {
		c := Contain(nil)
		if err := c.Terminate(); err != nil {
			t.Fatalf("Terminate() on nil-command containment = %v, want nil", err)
		}
		c.Release()
	})

	t.Run("command that was never started", func(t *testing.T) {
		// cmd.Process is nil until Start succeeds.
		c := Contain(exec.Command("sh", "-c", "true"))
		if err := c.Terminate(); err != nil {
			t.Fatalf("Terminate() on unstarted command = %v, want nil", err)
		}
		c.Release()
	})

	t.Run("release is idempotent and terminate after release is safe", func(t *testing.T) {
		c := Contain(nil)
		c.Release()
		c.Release()
		if err := c.Terminate(); err != nil {
			t.Fatalf("Terminate() after Release() = %v, want nil", err)
		}
	})

	t.Run("nil receiver", func(t *testing.T) {
		var c *Containment
		if err := c.Terminate(); err != nil {
			t.Fatalf("Terminate() on nil receiver = %v, want nil", err)
		}
		c.Release()
	})
}
