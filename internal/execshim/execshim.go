// Package execshim builds exec.Cmd values for user/pack-supplied script paths.
// Windows cannot fork/exec a `.sh` file directly ("%1 is not a valid Win32
// application"), so shell scripts are routed through `sh` (Git for Windows),
// which the Windows port already requires for agent launch wrappers. On other
// platforms this is a plain exec.Command.
package execshim

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// needsShell reports whether path must be interpreted by sh on this platform.
// A path that does not exist is left to direct exec so callers still see the
// standard file-not-found exec error instead of sh's exit 127 — gate and
// hook error classification depends on that distinction.
func needsShell(path string) bool {
	if runtime.GOOS != "windows" || !strings.EqualFold(filepath.Ext(path), ".sh") {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// ShPath resolves the sh interpreter. On PATH it is used as-is; otherwise on
// Windows the Git for Windows layout is derived from git.exe's location,
// because typical installs expose only mingw64\bin (git.exe) on PATH while
// sh.exe lives in <GitRoot>\usr\bin and <GitRoot>\bin. Falls back to "sh" so
// exec surfaces the original not-found error when nothing resolves.
var ShPath = sync.OnceValue(func() string {
	if p, err := exec.LookPath("sh"); err == nil {
		return p
	}
	if runtime.GOOS != "windows" {
		return "sh"
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return "sh"
	}
	// git.exe lives at <GitRoot>\cmd\git.exe or <GitRoot>\mingw64\bin\git.exe;
	// probe both possible roots for the sh.exe locations Git for Windows ships.
	for _, root := range []string{
		filepath.Dir(filepath.Dir(git)),               // <GitRoot>\cmd -> <GitRoot>
		filepath.Dir(filepath.Dir(filepath.Dir(git))), // <GitRoot>\mingw64\bin -> <GitRoot>
	} {
		for _, rel := range []string{
			filepath.Join("usr", "bin", "sh.exe"),
			filepath.Join("bin", "sh.exe"),
		} {
			cand := filepath.Join(root, rel)
			if info, statErr := os.Stat(cand); statErr == nil && !info.IsDir() {
				return cand
			}
		}
	}
	return "sh"
})

// Command is exec.Command with .sh routing on Windows.
func Command(path string, args ...string) *exec.Cmd {
	if needsShell(path) {
		return exec.Command(ShPath(), append([]string{path}, args...)...)
	}
	return exec.Command(path, args...)
}

// CommandContext is exec.CommandContext with .sh routing on Windows.
func CommandContext(ctx context.Context, path string, args ...string) *exec.Cmd {
	if needsShell(path) {
		return exec.CommandContext(ctx, ShPath(), append([]string{path}, args...)...)
	}
	return exec.CommandContext(ctx, path, args...)
}
