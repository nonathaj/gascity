//go:build windows

package main

import (
	"os/exec"
	"testing"
)

// startStandaloneBdDoltLikeProcess has no Windows implementation: the fake
// standalone dolt is built from bash `exec -a` + a FIFO (see the Unix arm),
// process-identity machinery Windows does not have.
func startStandaloneBdDoltLikeProcess(t *testing.T, _ string) *exec.Cmd {
	t.Helper()
	t.Skip("standalone-dolt fake needs bash exec -a and FIFOs (Unix only)")
	return nil
}
