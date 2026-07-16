//go:build windows

package subprocess

import (
	"os"
	"testing"
)

// requirePrivateSocketOwnership has no Unix mode/uid contract to enforce on
// Windows (NTFS ACLs on a per-user temp path); directory-ness is already
// asserted by the caller.
func requirePrivateSocketOwnership(_ *testing.T, _ string, _ os.FileInfo) {}
