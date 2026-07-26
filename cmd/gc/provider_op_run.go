package main

import (
	"bytes"
	"errors"
	"os/exec"
)

// runContainedProviderCommand starts cmd, places it in a platform containment
// scope, waits for it, and releases the scope.
//
// Use this instead of cmd.Run() for every command that executes pack-authored
// script content. Such content may background its work ("cmd &"), and on Windows
// the resulting grandchild is not reachable from the direct child's process tree at
// cancellation time — terminating sh.exe leaves it running for the life of the
// machine (gw-591; same class as the 93GB orphan incident gw-qhs). Job Objects are
// the platform's containment primitive and containProviderOpProcess applies one.
// Off Windows this is exactly cmd.Run(): prepareProviderOpCommand's Setpgid already
// makes the process group cover every descendant.
//
// Pair it with prepareProviderOpCommand, which installs the cancellation behavior;
// this function supplies the post-Start hook that assignment needs, which cmd.Run
// gives no room for.
func runContainedProviderCommand(cmd *exec.Cmd) error {
	if cmd == nil {
		return errors.New("runContainedProviderCommand: nil command")
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	release := containProviderOpProcess(cmd)
	err := cmd.Wait()
	release()
	return err
}

// outputContainedProviderCommand is runContainedProviderCommand for callers that
// want the child's stdout, replacing cmd.Output(). Like exec's own Output it
// requires cmd.Stdout to be unset.
//
// It deliberately does not reproduce Output's "populate ExitError.Stderr when
// cmd.Stderr is nil" behavior: every caller here sets Stderr explicitly, so that
// branch was already dead, and silently capturing stderr for callers who did not
// ask would change the errors they surface.
func outputContainedProviderCommand(cmd *exec.Cmd) ([]byte, error) {
	if cmd == nil {
		return nil, errors.New("outputContainedProviderCommand: nil command")
	}
	if cmd.Stdout != nil {
		return nil, errors.New("outputContainedProviderCommand: Stdout already set")
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	err := runContainedProviderCommand(cmd)
	return stdout.Bytes(), err
}
