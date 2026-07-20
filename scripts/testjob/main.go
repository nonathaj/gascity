// Command testjob contains a test command in a kill-on-close Windows
// Job Object — the analogue of the gascity-test.slice enrollment on
// Linux hosts (engdocs/design/windows-systemd-parity.md, D2). No member
// of the job can outlive the wrapper: a killed `go test` run takes its
// whole process tree with it instead of orphaning it (incidents gw-qhs,
// gw-8g5).
//
// Usage:
//
//	go run ./scripts/testjob -- go test ./internal/session/
//	go run ./scripts/testjob -count   # print recommended shard parallelism
//
// On non-Windows hosts the command runs unwrapped — containment there
// is owned by scripts/lib/test-slice.sh. GC_TEST_NO_SLICE=1 opts out;
// GC_TEST_JOB_MEMORY overrides the job memory cap in bytes.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/gastownhall/gascity/internal/execshim"
)

func main() {
	count := flag.Bool("count", false, "print recommended shard parallelism for the containment job and exit")
	flag.Parse()
	if *count {
		fmt.Println(jobCount())
		return
	}
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: testjob [-count] -- command [args...]")
		os.Exit(2)
	}
	os.Exit(run(args))
}

// proxy executes the command with inherited stdio and returns its exit
// code. Wrapper-level failures use 125 so they cannot be mistaken for
// the command's own exit codes.
func proxy(args []string) int {
	cmd := execshim.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "testjob: running %q: %v\n", args[0], err)
		return 125
	}
	return 0
}
