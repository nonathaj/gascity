// Package pidutil contains small process helpers shared across GC packages.
package pidutil

import (
	"runtime"
	"strings"
)

// AliveWithStartTime reports whether pid is alive AND still the same process
// identified by startTime. It closes the PID-reuse hole in Alive: during a
// post-SIGKILL reap wait the target's PID can be reaped and recycled to an
// unrelated new process inside the window, at which point plain Alive would
// wrongly report the (dead) target as still alive.
//
// An empty startTime disables the identity check and falls back to Alive — used
// on platforms without /proc start-time support (darwin, windows) or when the
// original start time could not be captured before the wait. A non-empty
// startTime that no longer matches means the PID was recycled: the original
// target is dead, so this returns false. When the current start time cannot be
// read despite Alive reporting true (a transient race, no /proc), it keeps the
// conservative Alive answer rather than inventing a death.
func AliveWithStartTime(pid int, startTime string) bool {
	if !Alive(pid) {
		return false
	}
	if startTime == "" {
		return true
	}
	current, err := StartTime(pid)
	if err != nil {
		return true
	}
	return current == startTime
}

// AliveWithCmdline reports whether a PID exists, is not a zombie, and its
// command line satisfies match. On platforms without /proc cmdline support it
// falls back to Alive so callers preserve existing non-Linux behavior.
func AliveWithCmdline(pid int, match func([]string) bool) bool {
	if !Alive(pid) {
		return false
	}
	if match == nil {
		return false
	}
	if runtime.GOOS != "linux" {
		return true
	}
	argv, err := Cmdline(pid)
	if err != nil {
		return false
	}
	return match(argv)
}

// ArgvContainsSequence reports whether argv contains seq contiguously.
func ArgvContainsSequence(argv []string, seq ...string) bool {
	if len(seq) == 0 {
		return true
	}
	if len(argv) < len(seq) {
		return false
	}
	for i := 0; i <= len(argv)-len(seq); i++ {
		ok := true
		for j := range seq {
			if argv[i+j] != seq[j] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// ArgvHasFlagValue reports whether argv contains flag with value, either as
// "--flag value" or "--flag=value".
func ArgvHasFlagValue(argv []string, flag, value string) bool {
	if flag == "" || value == "" {
		return false
	}
	for i, arg := range argv {
		if arg == flag && i+1 < len(argv) && argv[i+1] == value {
			return true
		}
		if strings.HasPrefix(arg, flag+"=") && strings.TrimPrefix(arg, flag+"=") == value {
			return true
		}
	}
	return false
}

// NormalizeArgv returns argv with empty and whitespace-only arguments
// dropped — the rule Cmdline applies to /proc command lines. Callers
// comparing a configured argv against Cmdline output must pass the
// configured side through this helper first so both sides share the same
// argument shape.
func NormalizeArgv(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, arg := range argv {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		out = append(out, arg)
	}
	return out
}
