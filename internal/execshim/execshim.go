// Package execshim builds exec.Cmd values for user/pack-supplied script paths.
// Windows cannot fork/exec a `.sh` file directly ("%1 is not a valid Win32
// application"), so shell scripts are routed through `sh` (Git for Windows),
// which the Windows port already requires for agent launch wrappers. On other
// platforms this is a plain exec.Command.
package execshim

import (
	"context"
	"io"
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
//
// Beyond the ".sh" extension, an existing file with a "#!" shebang whose
// extension Windows cannot execute natively (e.g. the repo's extensionless
// contrib scripts) is also routed through sh: direct exec of such a file can
// only ever fail on Windows, so shell routing is strictly an improvement and
// matches what the kernel would do on Unix.
func needsShell(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".sh":
		info, err := os.Stat(path)
		return err == nil && !info.IsDir()
	case ".exe", ".bat", ".cmd", ".com", ".ps1":
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // read-only sniff handle
	var magic [2]byte
	if n, _ := io.ReadFull(f, magic[:]); n != 2 {
		return false
	}
	return magic[0] == '#' && magic[1] == '!'
}

// ShPath resolves the sh interpreter. On PATH it is used as-is; otherwise on
// Windows the Git for Windows layout is derived from git.exe's location,
// because typical installs expose only mingw64\bin (git.exe) on PATH while
// sh.exe lives in <GitRoot>\usr\bin and <GitRoot>\bin. Falls back to "sh" so
// exec surfaces the original not-found error when nothing resolves.
func ShPath() string {
	return shPathMemo.resolve()
}

// shPathMemo memoizes the resolved sh path but ONLY caches a successful
// resolution. The unresolved "sh" fallback is never cached, so a call made
// while PATH is transiently narrowed (e.g. a test that scoped PATH via
// t.Setenv and happened to trigger the first resolution) cannot poison every
// later /bin/<coreutil> lookup for the whole process — the next call with a
// healthy environment re-resolves. (A plain sync.OnceValue would freeze the
// failure permanently.)
var shPathMemo shPathResolver

type shPathResolver struct {
	mu       sync.Mutex
	resolved string
}

func (r *shPathResolver) resolve() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.resolved != "" {
		return r.resolved
	}
	p := resolveShPath()
	// Only cache a real, absolute interpreter; leave the "sh" fallback
	// uncached so a later, healthier call can still resolve.
	if p != "sh" {
		r.resolved = p
	}
	return p
}

func resolveShPath() string {
	if p, err := exec.LookPath("sh"); err == nil {
		return p
	}
	if runtime.GOOS != "windows" {
		return "sh"
	}
	// Candidate Git-for-Windows roots. Prefer the one derived from git on PATH,
	// but ALSO probe the well-known install locations so a narrowed PATH (no
	// git) still resolves.
	var roots []string
	if git, err := exec.LookPath("git"); err == nil {
		roots = append(roots,
			filepath.Dir(filepath.Dir(git)),               // <GitRoot>\cmd -> <GitRoot>
			filepath.Dir(filepath.Dir(filepath.Dir(git))), // <GitRoot>\mingw64\bin -> <GitRoot>
		)
	}
	roots = append(roots, wellKnownGitForWindowsRoots()...)
	for _, root := range roots {
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
}

// wellKnownGitForWindowsRoots returns the standard Git for Windows install
// directories so sh resolution does not depend on git being on the current
// PATH (see ShPath's memoization note).
func wellKnownGitForWindowsRoots() []string {
	var roots []string
	seen := map[string]struct{}{}
	add := func(p string) {
		if p == "" {
			return
		}
		if _, ok := seen[p]; ok {
			return
		}
		seen[p] = struct{}{}
		roots = append(roots, p)
	}
	for _, base := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramW6432"),
		os.Getenv("LOCALAPPDATA"), // scoop / winget per-user installs live here
	} {
		if base == "" {
			continue
		}
		add(filepath.Join(base, "Git"))
		add(filepath.Join(base, "Programs", "Git"))
	}
	return roots
}

// shellScriptPath renders a script path the way a POSIX script expects to see
// its own name in $0 / ${BASH_SOURCE[0]}.
//
// Pack scripts locate their siblings with POSIX idioms such as
//
//	case "$0" in */*) dir="${0%/*}" ;; *) dir="$(pwd)" ;; esac
//
// Handed a native Windows path, that glob finds no forward slash, silently
// takes the fallback branch, and resolves the script's directory to the CALLER's
// working directory. Every core pack script that sources _bd_trace.sh does this,
// so orphan-sweep, reaper, jsonl-export and friends all failed with
// "_bd_trace.sh: No such file or directory" pointing at an unrelated directory.
//
// Normalizing here rather than teaching each script about backslashes is the
// point of this package: execshim owns the Windows/sh boundary so pack content
// stays pure POSIX sh. Slash-separated absolute paths are accepted by the shell
// and by Windows itself, and ToSlash is a no-op on Unix.
func shellScriptPath(path string) string {
	return filepath.ToSlash(path)
}

// Command is exec.Command with .sh routing on Windows.
func Command(path string, args ...string) *exec.Cmd {
	if needsShell(path) {
		cmd := exec.Command(ShPath(), append([]string{shellScriptPath(path)}, args...)...)
		cmd.Env = EnvWithShellDir(os.Environ())
		return cmd
	}
	if resolved, ok := resolveBareWindowsCommand(path); ok {
		cmd := exec.Command(resolved, args...)
		cmd.Env = EnvWithShellDir(os.Environ())
		return cmd
	}
	return exec.Command(path, args...)
}

// CommandContext is exec.CommandContext with .sh routing on Windows.
func CommandContext(ctx context.Context, path string, args ...string) *exec.Cmd {
	if needsShell(path) {
		cmd := exec.CommandContext(ctx, ShPath(), append([]string{shellScriptPath(path)}, args...)...)
		cmd.Env = EnvWithShellDir(os.Environ())
		return cmd
	}
	if resolved, ok := resolveBareWindowsCommand(path); ok {
		cmd := exec.CommandContext(ctx, resolved, args...)
		cmd.Env = EnvWithShellDir(os.Environ())
		return cmd
	}
	return exec.CommandContext(ctx, path, args...)
}

// resolveBareWindowsCommand resolves a bare command name (no path
// separator) to a runnable path on Windows, so an invocation of "sh"
// or a Git-for-Windows coreutil ("cat", "true", …) works where the
// bare name is not on the Windows PATH. Returns ("", false) when no
// resolution is needed: non-Windows, path-qualified names (handled by
// needsShell), or names exec already resolves via PATH/PATHEXT.
func resolveBareWindowsCommand(name string) (string, bool) {
	if runtime.GOOS != "windows" {
		return "", false
	}
	if strings.ContainsAny(name, `/\`) {
		return "", false
	}
	if strings.EqualFold(filepath.Base(name), "sh") {
		if sh := ShPath(); sh != name {
			return sh, true
		}
		return "", false
	}
	if _, err := exec.LookPath(name); err == nil {
		return "", false // exec already resolves it (real .exe/.bat on PATH)
	}
	if resolved, err := LookPath(name); err == nil {
		return resolved, true // coreutils fallback found it
	}
	return "", false
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
	base := name
	// On Windows a POSIX absolute coreutil path (/bin/echo, /usr/bin/true)
	// does not exist, but the tool ships in Git for Windows' coreutils dir
	// (the same dir sh lives in). Map such paths to the bare tool name so
	// portable configs and test stubs resolve there, like a bare name does.
	if runtime.GOOS == "windows" {
		if slash := filepath.ToSlash(name); strings.HasPrefix(slash, "/bin/") || strings.HasPrefix(slash, "/usr/bin/") {
			base = slash[strings.LastIndexByte(slash, '/')+1:]
		}
	}
	// Probe every candidate coreutils directory: the resolved sh interpreter's
	// dir first, then the well-known Git for Windows usr\bin / bin dirs. The
	// latter make resolution independent of ShPath being resolved at this
	// moment, so a transiently narrowed PATH cannot make /bin/<coreutil> fail.
	for _, dir := range coreutilDirs() {
		if !filepath.IsAbs(dir) {
			continue
		}
		cand := filepath.Join(dir, base)
		if runtime.GOOS == "windows" && !strings.EqualFold(filepath.Ext(base), ".exe") {
			cand += ".exe"
		}
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// coreutilDirs returns the directories that may hold Git for Windows coreutils,
// most-preferred first: the resolved sh interpreter's own directory, then the
// well-known install roots' usr\bin and bin.
func coreutilDirs() []string {
	dirs := []string{filepath.Dir(ShPath())}
	if runtime.GOOS == "windows" {
		for _, root := range wellKnownGitForWindowsRoots() {
			dirs = append(dirs, filepath.Join(root, "usr", "bin"), filepath.Join(root, "bin"))
		}
	}
	return dirs
}

// ResolveExecutable resolves name to a runnable path. Bare names go
// through LookPath (PATH plus the coreutils fallback). Path-qualified
// names resolve to themselves when they name an existing regular file:
// exec.LookPath would reject an extensionless script there on Windows
// (executability is PATHEXT-defined), but Command/CommandContext run
// shebang scripts through sh, so existence is the right bar.
func ResolveExecutable(name string) (string, error) {
	if !strings.ContainsAny(name, `/\`) {
		return LookPath(name)
	}
	info, err := os.Stat(name)
	if err != nil {
		return "", &exec.Error{Name: name, Err: err}
	}
	if info.IsDir() {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	return name, nil
}

// IsGoTestExecutable reports whether path looks like a `go test` binary.
// Go names test binaries "<pkg>.test" on Unix and "<pkg>.test.exe" on
// Windows; the comparison strips the Windows extension and ignores case
// (Windows filesystems are case-insensitive). Guards that stop gc from
// re-exec-ing "the gc binary" when it is really the test binary must use
// this: matching only the bare ".test" suffix passed Windows test
// binaries through, and a submit-poller spawn that re-ran the whole
// suite per spawn fork-bombed the host (incident gw-8g5, 4,500
// processes / ~246 GB commit in ~10 minutes). A false positive merely
// refuses a spawn or keeps a test hermetic; a false negative detonates,
// so the guard errs toward matching.
func IsGoTestExecutable(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	base = strings.TrimSuffix(base, ".exe")
	return strings.HasSuffix(base, ".test")
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
