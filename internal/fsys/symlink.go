package fsys

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
)

// errWinPrivilegeNotHeld is ERROR_PRIVILEGE_NOT_HELD (1314), the code
// Windows returns for unprivileged symlink creation when Developer Mode is
// off and the process is not elevated. Spelled as a raw errno so this file
// builds on every platform without a GOOS split.
const errWinPrivilegeNotHeld = syscall.Errno(1314)

// symlinkDevModeHint is appended to privilege failures so the operator
// learns the remediation instead of a bare "A required privilege is not
// held by the client".
const symlinkDevModeHint = "symlinks on Windows require Developer Mode " +
	"(Settings > System > For developers) or an elevated process"

// Symlink is os.Symlink with an actionable diagnosis on the Windows
// unprivileged-creation failure. Gas City links pack content into city and
// rig directories (formula resolution, skills materialization, instructions
// files); when that fails for privilege reasons the remediation must reach
// the operator.
func Symlink(oldname, newname string) error {
	return decorateSymlinkErr(runtime.GOOS, os.Symlink(oldname, newname))
}

// decorateSymlinkErr is the platform-parameterized core of Symlink's error
// handling, split out so both branches are testable from any build platform.
func decorateSymlinkErr(goos string, err error) error {
	if err == nil || goos != "windows" {
		return err
	}
	if errors.Is(err, errWinPrivilegeNotHeld) {
		return fmt.Errorf("%w (%s)", err, symlinkDevModeHint)
	}
	return err
}
