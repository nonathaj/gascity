---
title: "Windows Parity for systemd Capabilities"
---

| Field | Value |
|---|---|
| Status | Proposed |
| Date | 2026-07-20 |
| Author(s) | Claude |
| Issue | gw-5nk (spec), gw-184 (test containment), gw-qhs (incident) |
| Supersedes | N/A |

## Summary

Gas City leans on Linux user-systemd for four load-bearing jobs:
hosting the supervisor as a persistent auto-restarting service,
containing agent process trees in resource-limited slices, containing
test process trees in `gascity-test.slice`, and discovering/reaping
orphaned processes via `/proc` with subreaper awareness. On Windows
every one of these is a runtime-gated no-op or hard error today; the
only substitutes are a bare detached supervisor process and the
Toolhelp32 `proctable` layer.

This spec maps each systemd capability to a Windows-native mechanism —
**Job Objects** for containment, **Task Scheduler** for persistence, a
**named-pipe sd_notify transport** for readiness, and **PEB
env-reading proctable scans** for orphan discovery — behind one new
shared primitive package (`internal/winjob`). It fixes the class of
failure that produced incident gw-qhs (1,583 orphaned test binaries,
~93 GB of commit, host exhausted twice: Windows never tears down
process trees, and nothing on Windows played the role
`gascity-test.slice` plays on Linux). It also derives the test matrix:
which Linux-gated tests need Windows counterparts and which new
Windows-only tests must exist.

## Motivation

### The gap, concretely

The three-cluster survey (2026-07-20) found ~15 systemd touchpoints.
None are `//go:build`-gated; all compile into the Windows binary and
die at runtime via `supervisorRuntimeGOOS` switches, failed
`exec.LookPath("systemd-run")`, or absent `/proc`. What Windows loses:

| systemd capability | Linux mechanism | Windows today |
|---|---|---|
| Supervisor boot persistence | user unit, `WantedBy=default.target`, `loginctl enable-linger` | none — detached process dies at logout/reboot |
| Supervisor auto-restart | `Restart=always`, `RestartSec=5s` | none — only `gc start` drift-check respawn |
| Crash-loop suppression | `RestartPreventExitStatus=3` (port collision) | n/a (nothing restarts it) |
| Readiness / lifecycle signaling | sd_notify `READY/RELOADING/STOPPING/WATCHDOG` (`internal/sdnotify`) | silent no-op (`NOTIFY_SOCKET` unset) |
| Service install/uninstall + drift guard + stale-unit sweep | unit files under `~/.local/share/systemd/user/`, `systemctl --user` | `gc supervisor install` errors "not supported on windows" |
| Delegation to operator unit | `GC_SUPERVISOR_SYSTEMD_UNIT` | hard error (correct; keep) |
| Agent containment | `GC_AGENT_SLICE` → `systemd-run --user --scope --slice=…` per pane | warn-once, run unwrapped |
| Test containment | `gascity-test.slice` enrollment (`scripts/lib/test-slice.sh`) | inert (bash + systemd only) — **root cause of gw-qhs** |
| Test memory budgeting | cgroup `memory.max` via `scripts/test-local-job-count` | falls to static defaults |
| Orphan discovery | `/proc` scan, subreaper-aware (`ppid==1` or `systemd --user`) | silent no-op (`os.ReadDir("/proc")` fails) |
| Orphan/tree kill | `kill(-pgid)` | **already at parity** — `taskkill /T` + descendant sweep (`proctable/kill_windows.go`, `orphan_reap_windows.go`) |
| Warm-refresh workspace-service cleanup | `/proc/*/environ` scan | warning path; sweep unavailable |
| Log rotation | copy-then-truncate (manager holds O_APPEND fd) | **already at parity** (pure filesystem) |
| Logs | `journalctl` hints (never exec'd) | file tail works; hints skipped (fine) |

### Why now

1. **Incident gw-qhs** proved the containment gap is not theoretical:
   without a kill-on-close boundary, every killed `go test` run leaks
   its whole tree. The interim defenses (TestMain watchdog,
   self-expiring fixtures, an external reaper) are heuristics; a Job
   Object is the structural fix — the direct analogue of the cgroup.
2. **Full test parity is the standing goal.** ~50 supervisor-cluster
   tests are Linux-gated solely because their *mechanism* (systemctl,
   unixgram, `/proc`) is Linux-shaped, not because the *capability*
   is. Until Windows mechanisms exist, those capabilities have zero
   Windows coverage — and the capabilities themselves (persistence,
   restart, readiness) are exactly what a Windows deployment needs.
3. The survey shows the seams already exist: `supervisorRuntimeGOOS`
   switches, the `RestartHelpers` struct, the sdnotify wrapper, the
   proctable PEB reader. Parity is mostly filling `default:` branches,
   not re-architecting.

## Design

### D1. `internal/winjob` — the Job Object primitive (shared foundation)

One Windows-only package (`//go:build windows`) wrapping Job Objects,
consumed by both agent containment (D3) and test containment (D2).
API sketch:

```go
// Create makes (or opens) a named job with the given limits and
// returns a handle whose closure kills all members when
// KillOnClose is set.
func Create(name string, limits Limits) (*Job, error)

type Limits struct {
    KillOnClose  bool   // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
    DenyBreakaway bool  // omit JOB_OBJECT_LIMIT_BREAKAWAY_OK
    JobMemory    uint64 // JOBOBJECT_EXTENDED_LIMIT_INFORMATION.JobMemoryLimit
    CPURateWeight uint32 // JOBOBJECT_CPU_RATE_CONTROL_INFORMATION (weight-based)
}

func (j *Job) AssignCurrent() error      // AssignProcessToJobObject(self)
func (j *Job) Assign(pid int) error
func (j *Job) MemoryBudget() (max, used uint64, err error) // QueryInformationJobObject
func InJob(name string) (bool, error)   // nested-enrollment guard
```

Notes:
- Windows 8+ supports **nested jobs**, so wrapping is safe even when
  a terminal/CI runner already holds the process in a job.
- `CPURateWeight` is the analogue of slice `CPUWeight=`; `JobMemory`
  of `MemoryMax=`. Weight/soft-pressure parity with `MemoryHigh=`
  does not exist on Windows — we accept hard-cap semantics
  (documented divergence).
- No role names, no judgment: the package is pure transport (Layer 0
  side-effect code), matching the layering invariants.

### D2. Test containment — the `gascity-test.slice` analogue (gw-184, P0)

A Go wrapper binary replaces the bash enrollment library on Windows.
`scripts/lib/test-slice.sh` stays the Linux path unchanged; the two
meet at the same decision-matrix contract.

**Shape:** `go run ./scripts/testjob -- <command …>` (or a prebuilt
`testjob.exe` cached under `.gc/`), invoked by the Windows test
entrypoints (and available to agents driving `go test` directly):

1. Decision matrix, mirroring `gc_test_slice_should_wrap`:
   - `GC_TEST_NO_SLICE=1` → run plain (same opt-out env).
   - Already inside the gascity test job (`winjob.InJob`) → run plain
     (nested-runner guard; the analogue of the `/proc/self/cgroup`
     grep).
   - Otherwise create/open job `Local\gascity-test`, assign self,
     spawn the command as a child (inherits job membership), wait.
2. Limits: `KillOnClose` + `DenyBreakaway` + `JobMemory` from
   `GC_TEST_JOB_MEMORY` (default: 75% of `GlobalMemoryStatusEx`
   available). **DenyBreakaway is load-bearing:** `startDetached`
   deliberately uses `CREATE_BREAKAWAY_FROM_JOB` so real supervisors
   escape terminal jobs (`cmd_daemon_windows.go`), and it already has
   a no-breakaway fallback attr — denying breakaway in the test job
   keeps test-spawned supervisors contained without any production
   change.
3. Job counting: `testjob -count` prints the recommended shard
   parallelism from the job's memory budget
   (`min(cpus, jobMemory/4GiB, 16)`) — the `test-local-job-count`
   analogue, same constants.

Layered defenses stay: the TestMain watchdog (25 min hard `os.Exit`,
landed with gw-qhs) and the host reaper remain as backstops for runs
launched outside the wrapper.

### D3. Agent containment — `GC_AGENT_SLICE` analogue (P2)

Extend `agentSliceWrapper` (already probe-based with graceful
fallback) with a Windows arm: when `GC_AGENT_SLICE` is set on
Windows, instead of wrapping the pane command in `systemd-run`, the
provider assigns the pane process to job `Local\gascity-agents`.
Assignment must be race-free: a pane command forks immediately, and
children spawned before `AssignProcessToJobObject` escape the job
forever. Since psmux owns the spawn, the required sequence is
**create-suspended → assign → resume** (`CREATE_SUSPENDED`, then
`winjob.Assign`, then `ResumeThread`); descendants then inherit job
membership by construction. Post-hoc `Assign` on an already-running
PID is acceptable only for adopt/recovery paths, where the escape
window is documented. No command-string wrapping means no
`wrapperCommands`/descendant-walk changes on Windows. The probe
becomes "can I create/open the named job", preserving the warn-once
fallback contract. Resource pressure via `CPURateWeight`/`JobMemory`
read from optional `GC_AGENT_JOB_MEMORY`/`GC_AGENT_JOB_CPU_WEIGHT`
env (new GC_* vars must be added to the
`gc_env_read_baseline.golden` vocabulary gate).

### D4. Supervisor hosting — Task Scheduler + pipe readiness (P1)

**Persistence/install.** `gc supervisor install` on Windows creates a
per-`GC_HOME` Scheduled Task (`schtasks` or the Task Scheduler COM
API) named `GasCity Supervisor (<same sha1[:8] suffix as the unit
name>)`, logon-triggered, running `gc supervisor run` with the same
baked env the unit template carries (`GC_HOME`, `PATH`,
`GC_SUPERVISOR_PRESERVE_SESSIONS_ON_SIGNAL=1`, provider creds per
`processenv.IsProviderCredentialEnv`, same
`GC_SUPERVISOR_OMIT_PROVIDER_CREDS` opt-out). Parity mappings:
- Binary-drift guard: compare the task action's exe path against the
  current binary (analogue of `supervisorSystemdExecStartBinary`);
  refuse without `--force`.
- Stale isolated-unit sweep: enumerate `GasCity Supervisor (*)`
  tasks whose `GC_HOME` no longer exists; delete (analogue of
  `sweepStaleIsolatedSupervisorServices`, filling today's `default:`
  no-op).
- `loginctl enable-linger` analogue: logon trigger covers
  interactive hosts; boot persistence without logon requires the
  task to run whether-user-is-logged-on-or-not (password/S4U) — an
  operator choice surfaced as an install flag, not a default.
- **Crash-loop suppression caveat:** Task Scheduler restart-on-failure
  cannot filter exit codes, so it would loop a port-collision exit 3.
  Therefore the task does NOT use Task Scheduler restarts; restart
  stays owned by gc (next bullet).

**Auto-restart.** `Restart=always` cannot be delegated to something
running inside the supervisor (health patrol dies with it), and Task
Scheduler restart-on-failure cannot see exit codes. The parity
mechanism is a second, lightweight **keepalive task**: a repeating
trigger (every 5 minutes, matching `RestartSec` intent if not
latency) that runs `gc supervisor ensure` — a new idempotent command
that (a) exits immediately if the control socket answers, (b) refuses
to respawn while a port-collision sentinel from a recent exit-3 is
present (the `RestartPreventExitStatus=3` analogue, judgment kept in
gc), and (c) otherwise respawns via the existing `startDetached`
path. The drift-check Direct branch continues to handle
binary/pack-drift restarts on `gc start`. Residual divergence:
restart latency is minutes, not `RestartSec=5s` — documented, and
acceptable because the supervisor's own crash rate is the tail case
the keepalive covers.

**Readiness.** Add a Windows transport to `internal/sdnotify`: when
`GC_NOTIFY_PIPE` is set, `Notify` writes the same state datagrams
(`READY=1` …) to a named pipe instead of a unixgram socket. The
existing call sites (`notifySdState` at ready/reload/stopping/
watchdog points) are untouched; anything hosting the supervisor
(tests, a future wrapper, `gc start` waiting for readiness) can
listen. This turns the four sd_notify capabilities from dead code
into testable Windows behavior with a ~50-line transport.

### D5. Orphan discovery — `/proc` scan analogue (P3)

`workspacesvc.findOrphanedServiceProcesses` gets a Windows arm built
on what `proctable` already has: Toolhelp32 enumeration + PEB env
reads (`GC_SERVICE_NAME`/`GC_SERVICE_STATE_ROOT` matching). The
subreaper predicate translates as: a process is orphaned when its
recorded PPID is dead **or** the parent's creation time is newer than
the child's (PID reuse). That requires adding creation-time identity
(`GetProcessTimes`) to `pidutil` — which also closes the documented
PID-recycling caveat in `proctable/kill_windows.go`. The same scan
backs the warm-refresh workspace-service cleanup
(`cleanupSupervisorWorkspaceServicesForSupervisorStart`), removing
its "cleanup unavailable" warning path on Windows.

### Non-goals

- **Delegation** to an operator-owned Windows Service or task
  (`GC_SUPERVISOR_SYSTEMD_UNIT` analogue). The non-Linux hard error
  stays.
- **journald analogue** (Windows Event Log). File logs + rotation are
  already at parity; `gc supervisor logs` tails files everywhere.
- **Soft memory pressure** (`MemoryHigh=`) — Job Objects only hard-cap.
- **True Windows Service (SCM) hosting.** Scheduled Task covers the
  user-level deployment gc targets; SCM services run in session 0
  with a different identity model and would fork the provider stack.
  Revisit only if a server-deployment requirement appears.

## Phasing

| Phase | Deliverable | Beads |
|---|---|---|
| P0 | `internal/winjob` + `testjob` wrapper + entrypoint wiring | gw-184 |
| P1 | Task Scheduler install/uninstall/sweep + `GC_NOTIFY_PIPE` sdnotify transport | new |
| P2 | Agent job containment (`GC_AGENT_SLICE` Windows arm) | new |
| P3 | Orphan-scan Windows arm + `pidutil` creation-time identity | new |

## Test matrix (what this unblocks)

**New Windows-only tests (per phase):**
- P0: job kill-on-close kills a spawned tree when the wrapper dies;
  nested-enrollment guard (`InJob` short-circuit); breakaway denied —
  a `startDetached`-style spawn stays in the job (falls to the
  no-breakaway attr); `testjob -count` respects `JobMemory`;
  `GC_TEST_NO_SLICE=1` opt-out. (Mirrors the 8-scenario bash
  decision-matrix self-test `scripts/test-slice-enroll-test`.)
- P1: install creates the task with correct action/env; drift guard
  refuses foreign binary without `--force`; stale-task sweep removes
  vanished-`GC_HOME` tasks and spares live ones; `gc supervisor
  ensure` no-ops on a healthy supervisor, refuses to respawn while
  the exit-3 port-collision sentinel is fresh, and respawns a killed
  supervisor; sd_notify pipe transport emits
  `READY→(RELOADING→READY)→STOPPING` (port of
  `cmd_supervisor_sdnotify_test.go`, today unixgram+SIGHUP-bound).
- P2: pane PID lands in the agents job when `GC_AGENT_SLICE` set;
  probe-failure warn-once fallback (port of
  `TestAgentSliceProbeFailureFallsBackPlainWithWarning` semantics).
- P3: orphan scan finds a dead-parent process by PEB env match and
  ignores identity mismatches (ports of the `//go:build !windows`
  `orphan_reap_test.go` suite, including
  `TestParentIsSubreaper`-equivalent predicate tests); PID-reuse
  creation-time guard.

**Existing Linux-gated tests that gain Windows counterparts:**
`cmd_supervisor_sdnotify_test.go` (4 lifecycle tests),
`cmd_supervisor_install_guard_test.go`,
`cmd_supervisor_sentinel_unix_test.go` (warm-refresh cleanup),
`orphan_reap_test.go` (whole file), the start-drift systemd
integration scenario (Task Scheduler variant), and the
`test-slice-enroll-test` matrix (Go variant).

**Already fixed alongside this spec:** `scripts/test_slice_enroll_test.go`
was the one ungated Windows exposure (exec'd a bash script on
`go test ./scripts`); it now skips on Windows with a pointer here.

## Survey provenance

Full touchpoint inventory (file:line per capability) from the
three-cluster survey, 2026-07-20: supervisor/service hosting
(`cmd/gc/cmd_supervisor*.go`, `supervisor_systemd_delegate.go`,
`internal/sdnotify`), runtime containment
(`internal/runtime/tmux/agent_slice.go`,
`internal/workspacesvc/orphan_reap*.go`, `internal/runtime/proctable`),
test harness (`scripts/lib/test-slice.sh`, `scripts/test-local-job-count`,
Makefile parallel targets, `rc-gate.yml` `GC_TEST_NO_SLICE`).
Notable negative findings: no code writes `/sys/fs/cgroup` directly;
`internal/doctor` uses `lsof`/`/proc` (not systemctl);
`internal/convergence` and `beads_provider_lifecycle.go` have no
systemd usage (scope-word false positives); the Dolt "live-session
probe" is SQL `information_schema.processlist`, not process
enumeration.
