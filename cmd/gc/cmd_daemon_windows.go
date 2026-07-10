//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/gastownhall/gascity/internal/execshim"
)

// Windows creation flags (Win32) for the detached supervisor. It must not
// share the launcher's console: console-close would terminate it — observed
// when `gc start` ran inside an embedded terminal. CREATE_NO_WINDOW (not
// DETACHED_PROCESS) is essential: the supervisor spawns console children
// (psmux/tmux), and with no console of its own every child would allocate a
// fresh visible console window; CREATE_NO_WINDOW gives it a hidden console
// that children inherit. It should also escape any kill-on-close job the
// launcher lives in.
const (
	winCreateNewProcessGroup  = 0x00000200
	winCreateBreakawayFromJob = 0x01000000
	winCreateNoWindow         = 0x08000000
)

func backgroundSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: winCreateNoWindow | winCreateNewProcessGroup,
		HideWindow:    true,
	}
}

// supervisorEnv returns the environment for the detached supervisor. gc's
// runtime shells out to `sh` for setup hooks, pack scripts and agent launch
// wrappers; when the launcher's PATH doesn't carry Git for Windows' usr\bin
// (e.g. a GUI cockpit spawning `gc start`), resolve sh explicitly and put its
// directory on PATH for the whole supervisor process tree.
func supervisorEnv() []string {
	env := os.Environ()
	shDir := filepath.Dir(execshim.ShPath())
	if !filepath.IsAbs(shDir) {
		return env
	}
	norm := func(p string) string { return strings.ToLower(strings.TrimRight(p, `\`)) }
	for i, kv := range env {
		if len(kv) < 5 || !strings.EqualFold(kv[:5], "PATH=") {
			continue
		}
		for _, p := range strings.Split(kv[5:], string(os.PathListSeparator)) {
			if norm(p) == norm(shDir) {
				return env
			}
		}
		env[i] = kv + string(os.PathListSeparator) + shDir
		return env
	}
	return append(env, "PATH="+shDir)
}

// detachedSupervisorAttrs returns attr candidates in preference order:
// breakaway-from-job first (requires the parent job to allow it), then the
// plain no-window form as a fallback.
func detachedSupervisorAttrs() []*syscall.SysProcAttr {
	return []*syscall.SysProcAttr{
		{
			CreationFlags: winCreateNoWindow | winCreateNewProcessGroup |
				winCreateBreakawayFromJob,
			HideWindow: true,
		},
		{
			CreationFlags: winCreateNoWindow | winCreateNewProcessGroup,
			HideWindow:    true,
		},
	}
}
