//go:build windows

package pidutil

import (
	"os/exec"
	"strconv"
)

// KillTree terminates pid and every process descended from it.
//
// Windows has no process groups in the POSIX sense, so the Unix idiom
// (Setpgid at spawn, then kill(-pgid)) has no equivalent: TerminateProcess
// affects exactly one process. That matters because gc runs pack scripts
// through sh.exe (execshim), so the work — dolt, bd — is always a GRANDCHILD.
// Killing only the direct child leaves those alive, holding pipes open and
// leaking processes for the lifetime of the machine.
//
// taskkill /T walks the parent links and /F forces termination. It is
// fire-and-forget: it does not wait for confirmed death (that is WaitDelay's
// job at the call site), so cancellation returns promptly. Errors are
// non-fatal — the usual one is "process already gone", and the direct Kill
// below is the backstop.
func KillTree(pid int) error {
	if pid <= 0 {
		return nil
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(pid)).Run()
	return nil
}
