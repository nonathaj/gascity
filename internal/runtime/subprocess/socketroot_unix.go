//go:build !windows

package subprocess

import (
	"fmt"
	"path/filepath"
)

// platformPrivateFallbackRoot returns the short per-user root for control
// sockets whose natural path would exceed sun_path: /tmp is world-writable
// with the sticky bit, so the dir is namespaced by euid and its ownership is
// validated (validatePrivateSocketOwnership).
func platformPrivateFallbackRoot(euid int) string {
	return filepath.Join(shortSocketTempRoot, fmt.Sprintf("%s-%d", fallbackSocketDirName, euid))
}
