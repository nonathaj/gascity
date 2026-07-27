package orders

import (
	"os/exec"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/processgroup"
)

var conditionProcessGroupOptions processgroup.Options

// conditionCommand runs a condition check so that nothing it spawns can outlive it.
//
// This deliberately has no per-platform variant. It used to: the Unix build tore down the
// whole process group, while the Windows build called cmd.Process.Kill() — killing only the
// direct child. A check like `sh -c '...' &` backgrounds its real work, so killing the direct
// child left that work running forever. The test meant to catch it inferred death from a
// heartbeat file that, on Windows, had never started growing, so it reported a confusing
// timeout instead of the leak. A degraded stub behind a build tag is how that stayed
// invisible; one implementation over a platform-abstracted primitive is why processgroup
// exists.
type conditionCommand struct {
	cmd            *exec.Cmd
	cleanupTimeout time.Duration

	mu          sync.Mutex
	containment *processgroup.Containment
	cleanupErr  error
	cleanupOnce sync.Once
}

// prepareConditionCommand puts a condition check in its own process group and installs
// cmd.Cancel so a context timeout runs the same teardown as an explicit cleanup.
func prepareConditionCommand(cmd *exec.Cmd, cleanupTimeout time.Duration) *conditionCommand {
	processgroup.StartCommandInNewGroup(cmd)
	c := &conditionCommand{cmd: cmd, cleanupTimeout: cleanupTimeout}
	cmd.Cancel = func() error {
		_ = c.Cleanup()
		return nil
	}
	return c
}

// Run starts the command, contains it, and waits for it to finish.
//
// Start and Wait are split rather than using cmd.Run because containment can only be
// attached to a live process, and it has to be attached before the command's own shell has a
// chance to exit. Waiting until cancellation is too late: by then the intermediate parent is
// gone, which is the whole failure being fixed.
func (c *conditionCommand) Run() error {
	if err := c.cmd.Start(); err != nil {
		return err
	}
	c.mu.Lock()
	c.containment = processgroup.Contain(c.cmd)
	c.mu.Unlock()
	return c.cmd.Wait()
}

// Cleanup tears down the process group and the containment. It runs at most once and
// returns the same result to every caller.
func (c *conditionCommand) Cleanup() error {
	c.cleanupOnce.Do(func() {
		knownPGID := 0
		if c.cmd != nil && c.cmd.Process != nil {
			knownPGID = c.cmd.Process.Pid
		}
		err := processgroup.TerminateCommand(c.cmd, knownPGID, c.cleanupTimeout, conditionProcessGroupOptions)
		c.mu.Lock()
		// Terminate the containment as well: on Windows the taskkill above cannot reach a
		// descendant whose intermediate parent has already exited, and the job can.
		if c.containment != nil {
			if termErr := c.containment.Terminate(); termErr != nil && err == nil {
				err = termErr
			}
			c.containment.Release()
			c.containment = nil
		}
		c.cleanupErr = err
		c.mu.Unlock()
	})
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cleanupErr
}
