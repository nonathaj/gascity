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
		cmd := exec.Command(ShPath(), append([]string{path}, args...)...)
		cmd.Env = EnvWithShellDir(os.Environ())
		return cmd
	}
	return exec.Command(path, args...)
}

// CommandContext is exec.CommandContext with .sh routing on Windows.
func CommandContext(ctx context.Context, path string, args ...string) *exec.Cmd {
	if needsShell(path) {
		cmd := exec.CommandContext(ctx, ShPath(), append([]string{path}, args...)...)
		cmd.Env = EnvWithShellDir(os.Environ())
		return cmd
	}
	return exec.CommandContext(ctx, path, args...)
}

// ShellCommand builds `sh -c command` with the resolved interpreter, so a
// shell command line works on Windows hosts where sh.exe is not on PATH but
// Git for Windows is installed.
func ShellCommand(command string) *exec.Cmd {
	cmd := exec.Command(ShPath(), "-c", command)
	cmd.Env = EnvWithShellDir(os.Environ())
	return cmd
}

// ShellCommandContext is ShellCommand with a context.
func ShellCommandContext(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, ShPath(), "-c", command)
	cmd.Env = EnvWithShellDir(os.Environ())
	return cmd
}

// LookPath resolves name like exec.LookPath, falling back to the resolved sh
// interpreter's directory — Git for Windows ships the coreutils (tail, head,
// cat, ...) alongside sh.exe in usr\bin, which a typical Windows PATH does not
// expose. Callers that exec a coreutil directly (not through sh) use this so
// the binary resolves on any host where gc's shell execution works at all.
func LookPath(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	dir := filepath.Dir(ShPath())
	if filepath.IsAbs(dir) {
		cand := filepath.Join(dir, name)
		if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(name), ".exe") {
			cand += ".exe"
		}
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// EnvWithShellDir returns env (a KEY=VALUE slice as from os.Environ) with the
// resolved sh interpreter's directory ensured on PATH. Scripts routed through
// sh on Windows invoke coreutils (cat, grep, sed, ...) that ship in the same
// Git-for-Windows directory as sh.exe; a typical install exposes only
// mingw64\bin (git.exe) on PATH, so without this those utilities are not found
// and shell providers/hooks fail with "command not found". No-op on non-Windows
// and when sh has no absolute directory (the plain "sh" fallback) or the dir is
// already present. Callers that build a custom child environment (setting
// cmd.Env explicitly after Command/ShellCommand) should wrap it with this.
func EnvWithShellDir(env []string) []string {
	if runtime.GOOS != "windows" {
		return env
	}
	dir := filepath.Dir(ShPath())
	if !filepath.IsAbs(dir) {
		return env
	}
	norm := func(p string) string { return strings.ToLower(strings.TrimRight(p, `\`)) }
	target := norm(dir)
	for i, kv := range env {
		if len(kv) < 5 || !strings.EqualFold(kv[:5], "PATH=") {
			continue
		}
		for _, p := range strings.Split(kv[5:], string(os.PathListSeparator)) {
			if norm(p) == target {
				return env
			}
		}
		out := append([]string(nil), env...)
		out[i] = kv[:5] + dir + string(os.PathListSeparator) + kv[5:]
		return out
	}
	return append(append([]string(nil), env...), "PATH="+dir)
}
