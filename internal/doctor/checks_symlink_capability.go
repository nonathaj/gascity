package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// SymlinkCapabilityCheck probes whether the process can create symbolic
// links. Formula resolution, skills materialization, and instructions-file
// recovery all link pack content into city and rig directories; on Windows
// unprivileged symlink creation needs Developer Mode (or elevation), and
// without it those subsystems hard-fail at dispatch time. The probe turns
// that late failure into an up-front diagnostic.
type SymlinkCapabilityCheck struct {
	// probeDir overrides where the probe link is created (tests). Empty
	// means os.TempDir().
	probeDir string
	// symlink is the link-creation seam (tests inject failures). Nil
	// means os.Symlink.
	symlink func(oldname, newname string) error
}

// NewSymlinkCapabilityCheck creates the symlink capability check.
func NewSymlinkCapabilityCheck() *SymlinkCapabilityCheck {
	return &SymlinkCapabilityCheck{}
}

// Name implements Check.
func (*SymlinkCapabilityCheck) Name() string { return "symlink-capability" }

// CanFix implements Check. Enabling Developer Mode or elevating is an
// operator action, not something gc can remediate.
func (*SymlinkCapabilityCheck) CanFix() bool { return false }

// Fix implements Check.
func (*SymlinkCapabilityCheck) Fix(*CheckContext) error { return nil }

// WarmupEligible implements Check.
func (*SymlinkCapabilityCheck) WarmupEligible() bool { return false }

// Run implements Check: create and remove a dangling symlink in the temp
// directory. The probe is a single syscall pair and needs no target file
// (symlink creation does not require the target to exist).
func (c *SymlinkCapabilityCheck) Run(_ *CheckContext) *CheckResult {
	r := &CheckResult{Name: c.Name()}

	dir := c.probeDir
	if dir == "" {
		dir = os.TempDir()
	}
	symlink := c.symlink
	if symlink == nil {
		symlink = os.Symlink
	}

	link := filepath.Join(dir, fmt.Sprintf(".gc-symlink-probe-%d", os.Getpid()))
	_ = os.Remove(link) // stale probe from a crashed prior run
	err := symlink("gc-symlink-probe-target", link)
	if err == nil {
		_ = os.Remove(link)
		r.Status = StatusOK
		r.Message = "symbolic link creation available"
		return r
	}

	r.Status = StatusError
	r.Message = "cannot create symbolic links"
	r.Details = []string{err.Error()}
	if runtime.GOOS == "windows" {
		r.FixHint = "enable Developer Mode (Settings > System > For developers) or run gc elevated; " +
			"formula resolution and skills materialization require symlinks"
	} else {
		r.FixHint = "verify the filesystem hosting the city supports symbolic links"
	}
	return r
}
