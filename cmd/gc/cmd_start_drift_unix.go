//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// readSupervisorExePath returns the resolved path of the supervisor's
// executable via /proc/<pid>/exe. The kernel readlink resolves
// symlinks for us — no extra realpath layer needed.
//
// When the binary on disk has been replaced (the typical drift case:
// `go install` writes a new file at the same path, unlinking the
// original inode the supervisor still has open), the kernel decorates
// the link target with a literal " (deleted)" suffix. We strip that
// suffix because the on-disk path is what the auto-restart needs to
// spawn — the new bytes already live at the un-suffixed path.
func readSupervisorExePath(pid int) (string, error) {
	target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(target, " (deleted)"), nil
}
