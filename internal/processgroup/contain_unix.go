//go:build !windows

package processgroup

import "os/exec"

// Containment is a no-op on Unix: a process group already provides durable grouping.
//
// The group id outlives its leader, so kill(-pgid) reaches descendants that have been
// orphaned and re-parented to init. StartCommandInNewGroup plus Terminate is therefore
// already sufficient here, and there is nothing to add. The type exists so callers can be
// written once instead of forked per platform — the divergence between a full Unix
// implementation and a degraded Windows stub is what let the leak this fixes go unnoticed.
type Containment struct{}

// Contain returns an inert containment; Unix needs no extra grouping.
func Contain(cmd *exec.Cmd) *Containment { return &Containment{} }

// Terminate does nothing; Terminate on the process group already covers this.
func (c *Containment) Terminate() error { return nil }

// Release does nothing; there is no handle to drop.
func (c *Containment) Release() {}
