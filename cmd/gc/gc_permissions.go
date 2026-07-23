package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/gastownhall/gascity/internal/winsec"
)

// gcDirPerm is the enforced permission for the .gc/ runtime directory.
const gcDirPerm = 0o700

// enforceGCPermissions ensures the .gc/ directory and its sensitive
// subdirectories have restrictive permissions. Called at controller
// startup to tighten any directories created with looser defaults.
//
// Enforced permissions:
//   - .gc/          → 0700
//   - .gc/secrets/  → 0700
func enforceGCPermissions(cityPath string, stderr io.Writer) {
	gcDir := filepath.Join(cityPath, ".gc")
	chmodIfExists(gcDir, gcDirPerm, stderr)
	chmodIfExists(filepath.Join(gcDir, secretsDir), secretsDirPerm, stderr)
}

// chmodIfExists sets the permission on path if it exists. Logs errors
// to stderr but does not fail — permission enforcement is best-effort.
func chmodIfExists(path string, perm os.FileMode, stderr io.Writer) {
	info, err := os.Stat(path)
	if err != nil {
		return // doesn't exist yet — will be created with correct perms
	}
	if !info.IsDir() {
		return
	}
	if info.Mode().Perm() != perm {
		if err := os.Chmod(path, perm); err != nil {
			fmt.Fprintf(stderr, "gc: chmod %s to %o: %v\n", path, perm, err) //nolint:errcheck
		}
	}
	// os.Chmod cannot revoke access on Windows — it only toggles the read-only
	// bit — so a 0700 chmod leaves the directory readable by every local user.
	// Apply a restrictive owner-only DACL there so the enforcement is real. This
	// is a no-op on Unix, where the chmod above is authoritative.
	if err := winsec.RestrictToOwner(path); err != nil {
		fmt.Fprintf(stderr, "gc: restrict %s: %v\n", path, err) //nolint:errcheck
	}
}
