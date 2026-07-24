//go:build windows

package subprocess

import (
	"os"
	"path/filepath"
)

// platformPrivateFallbackRoot returns the short per-user root for control
// sockets whose natural path would exceed the AF_UNIX sun_path limit. The
// Unix "/tmp/<name>-<euid>" form is wrong here twice over: \tmp usually does
// not exist (the mkdir fails on hosted runners) and euid is always -1.
// LOCALAPPDATA is guaranteed, per-user (no euid namespacing needed — ACLs
// already scope it), and short enough (~38 chars) to keep socket paths under
// the limit.
func platformPrivateFallbackRoot(int) string {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			root = filepath.Join(home, "AppData", "Local")
		} else {
			root = os.TempDir()
		}
	}
	return filepath.Join(root, fallbackSocketDirName)
}
