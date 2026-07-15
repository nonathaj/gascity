package fsys

import (
	"io/fs"
	"runtime"
)

// IsExecutableMode reports whether a file with the given mode should be
// treated as an executable program on this platform. On Unix this is the
// conventional perm&0o111 check. Windows has no Unix execute bits — os.Stat
// never sets them — so any regular file qualifies: binaries are .exe files
// resolved by CreateProcess and scripts run through the execshim.
func IsExecutableMode(mode fs.FileMode) bool {
	return isExecutableMode(runtime.GOOS, mode)
}

// isExecutableMode is the platform-parameterized core of IsExecutableMode,
// split out so both branches are testable from any build platform.
func isExecutableMode(goos string, mode fs.FileMode) bool {
	if !mode.IsRegular() {
		return false
	}
	if goos == "windows" {
		return true
	}
	return mode.Perm()&0o111 != 0
}
