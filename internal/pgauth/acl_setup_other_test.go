//go:build !windows

package pgauth

import (
	"os"
	"testing"
)

// applyUnixModeAsWindowsACL is a no-op on non-Windows platforms: os.Chmod
// already applied the mode, which is the authoritative permission there.
func applyUnixModeAsWindowsACL(*testing.T, string, os.FileMode) {}
