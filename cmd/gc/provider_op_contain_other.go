//go:build !windows

package main

import "os/exec"

// containProviderOpProcess is a no-op off Windows: prepareProviderOpCommand puts
// the child in its own process group (Setpgid) and cancellation kills the group,
// which already contains every descendant.
func containProviderOpProcess(_ *exec.Cmd) func() { return func() {} }
