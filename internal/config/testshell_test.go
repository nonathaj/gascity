package config

import (
	"os"
	"os/exec"
	"runtime"

	"github.com/gastownhall/gascity/internal/execshim"
)

// testShellCommand builds `sh -c shellCmd` for tests that exercise rendered
// work-query / hook / dispatcher shell lines against fake bd/gc scripts.
// pathPrepend is joined with the inherited PATH using the platform list
// separator (a hardcoded ':' corrupts PATH on Windows), the interpreter is
// resolved through the execshim (sh.exe is not on a stock Windows PATH),
// and on Windows the system identity vars msys sh needs are carried over.
func testShellCommand(shellCmd, pathPrepend string, extraEnv ...string) *exec.Cmd {
	cmd := execshim.ShellCommand(shellCmd)
	env := []string{"PATH=" + pathPrepend + string(os.PathListSeparator) + os.Getenv("PATH")}
	env = append(env, extraEnv...)
	cmd.Env = testShellEnv(env)
	return cmd
}

// testShellEnv completes an explicit test environment so a generated shell
// line can run under it on every platform: on Windows the system variables sh
// and its coreutils need are carried over, and sh's own directory
// (Git-for-Windows usr\bin, which also holds the coreutils the rendered shell
// lines invoke) survives the PATH override. On Unix it is the identity.
func testShellEnv(env []string) []string {
	out := append([]string(nil), env...)
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "SystemDrive", "ComSpec", "PATHEXT", "TEMP", "TMP"} {
			if v := os.Getenv(key); v != "" {
				out = append(out, key+"="+v)
			}
		}
	}
	return execshim.EnvWithShellDir(out)
}
