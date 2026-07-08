// Package execshim builds exec.Cmd values for user/pack-supplied script paths.
// Windows cannot fork/exec a `.sh` file directly ("%1 is not a valid Win32
// application"), so shell scripts are routed through `sh` (Git for Windows),
// which the Windows port already requires for agent launch wrappers. On other
// platforms this is a plain exec.Command.
package execshim

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// needsShell reports whether path must be interpreted by sh on this platform.
func needsShell(path string) bool {
	return runtime.GOOS == "windows" &&
		strings.EqualFold(filepath.Ext(path), ".sh")
}

// Command is exec.Command with .sh routing on Windows.
func Command(path string, args ...string) *exec.Cmd {
	if needsShell(path) {
		return exec.Command("sh", append([]string{path}, args...)...)
	}
	return exec.Command(path, args...)
}

// CommandContext is exec.CommandContext with .sh routing on Windows.
func CommandContext(ctx context.Context, path string, args ...string) *exec.Cmd {
	if needsShell(path) {
		return exec.CommandContext(ctx, "sh", append([]string{path}, args...)...)
	}
	return exec.CommandContext(ctx, path, args...)
}
