package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

const (
	// detachedProbeMetadataKey is a work-bead metadata contract documented in
	// engdocs/architecture/health-patrol.md. Values use tmux:<socket>:<session>.
	detachedProbeMetadataKey    = beadmeta.DetachedMetadataKey
	detachedProbeErrorThreshold = 3
)

// detachedProbeDefaultTimeout bounds a single `tmux has-session` spawn. One
// second is ample on Unix, where a fork+exec is sub-millisecond, but Windows
// has no copy-on-write fork: MSYS emulates it by copying the address space and
// replaying DLL base addresses, so spawning a process costs ~380ms before it
// runs at all and considerably more when the machine is busy. At one second the
// probe reported "timeout" for sessions that were plainly alive or dead, and
// pool_session_name.go treats a timeout as an error. The Windows budget is
// deliberately several spawns wide; it bounds a hang, and a probe that answers
// late is still far better than one that answers wrongly.
var detachedProbeDefaultTimeout = func() time.Duration {
	if runtime.GOOS == "windows" {
		return 5 * time.Second
	}
	return time.Second
}()

type detachedProbeStatus string

const (
	detachedProbeAlive   detachedProbeStatus = "alive"
	detachedProbeDead    detachedProbeStatus = "dead"
	detachedProbeError   detachedProbeStatus = "error"
	detachedProbeTimeout detachedProbeStatus = "timeout"
)

type detachedProbeSpec struct {
	Kind    string
	Socket  string
	Session string
}

type detachedProbeResult struct {
	Status detachedProbeStatus
	Spec   detachedProbeSpec
	Err    error
}

var detachedProbeErrorCounts = struct {
	sync.Mutex
	byBead map[string]int
}{
	byBead: make(map[string]int),
}

func parseDetachedProbeSpec(spec string) (detachedProbeSpec, error) {
	parts := strings.SplitN(strings.TrimSpace(spec), ":", 3)
	if len(parts) != 3 {
		return detachedProbeSpec{}, fmt.Errorf("parsing detached probe spec %q: want tmux:<socket>:<session>", spec)
	}
	kind := strings.TrimSpace(parts[0])
	socket := strings.TrimSpace(parts[1])
	session := strings.TrimSpace(parts[2])
	if kind != "tmux" {
		return detachedProbeSpec{}, fmt.Errorf("parsing detached probe spec %q: unsupported kind %q", spec, kind)
	}
	if socket == "" || session == "" {
		return detachedProbeSpec{}, fmt.Errorf("parsing detached probe spec %q: socket and session are required", spec)
	}
	return detachedProbeSpec{
		Kind:    kind,
		Socket:  socket,
		Session: session,
	}, nil
}

func probeDetachedWork(ctx context.Context, spec string) detachedProbeResult {
	return probeDetachedWorkWithTimeout(ctx, spec, detachedProbeDefaultTimeout)
}

func probeDetachedWorkWithTimeout(ctx context.Context, spec string, timeout time.Duration) detachedProbeResult {
	parsed, err := parseDetachedProbeSpec(spec)
	if err != nil {
		return detachedProbeResult{Status: detachedProbeError, Err: err}
	}
	if timeout <= 0 {
		timeout = detachedProbeDefaultTimeout
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, "tmux", "-L", parsed.Socket, "has-session", "-t", parsed.Session)
	if err := cmd.Run(); err != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return detachedProbeResult{Status: detachedProbeTimeout, Spec: parsed, Err: probeCtx.Err()}
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return detachedProbeResult{Status: detachedProbeDead, Spec: parsed}
		}
		return detachedProbeResult{Status: detachedProbeError, Spec: parsed, Err: err}
	}
	return detachedProbeResult{Status: detachedProbeAlive, Spec: parsed}
}

func incrementDetachedProbeErrorCount(id string) int {
	detachedProbeErrorCounts.Lock()
	defer detachedProbeErrorCounts.Unlock()
	if detachedProbeErrorCounts.byBead == nil {
		detachedProbeErrorCounts.byBead = make(map[string]int)
	}
	detachedProbeErrorCounts.byBead[id]++
	return detachedProbeErrorCounts.byBead[id]
}

func clearDetachedProbeErrorCount(id string) {
	detachedProbeErrorCounts.Lock()
	defer detachedProbeErrorCounts.Unlock()
	delete(detachedProbeErrorCounts.byBead, id)
}

func resetDetachedProbeErrorCountsForTest() {
	detachedProbeErrorCounts.Lock()
	defer detachedProbeErrorCounts.Unlock()
	detachedProbeErrorCounts.byBead = make(map[string]int)
}
