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
	cmd.Env = []string{"PATH=" + pathPrepend + string(os.PathListSeparator) + os.Getenv("PATH")}
	cmd.Env = append(cmd.Env, extraEnv...)
	if runtime.GOOS == "windows" {
		for _, key := range []string{"SystemRoot", "SystemDrive", "ComSpec", "PATHEXT", "TEMP", "TMP"} {
			if v := os.Getenv(key); v != "" {
				cmd.Env = append(cmd.Env, key+"="+v)
			}
		}
	}
	// Ensure sh's own directory (Git-for-Windows usr\bin, which also holds the
	// coreutils the rendered shell lines invoke) survives the PATH override.
	cmd.Env = execshim.EnvWithShellDir(cmd.Env)
	return cmd
}
