# Windows pid space: the missing boundary contract

**Status:** DRAFT for review. Not implemented. Filed as gw-dbm (P1).

## 1. The defect, in one sentence

The sh↔Go boundary carries a `pid` field with no declared numbering space, so
Tier‑2 sh writes an **MSYS pid** and Tier‑1 Go reads it as a **native Windows
pid** — each side is locally correct and the pair is jointly wrong.

## 2. Evidence (measured, not inferred)

Backgrounding a *native* Windows child from Git‑for‑Windows sh exactly the way
`gc-beads-bd.sh` backgrounds dolt:

```sh
nohup cmd //c "ping -n 30 127.0.0.1" >/dev/null 2>&1 &
p=$!
# shell_reported_pid=46077
# ps -W: msyspid=46077  winpid=323084  cmd=/c/WINDOWS/system32/cmd
# nothing matches 46077 in the WINPID column
```

`$!` is an MSYS pid even for a native child. Same for `$$` (measured separately:
sh reported 10782; the real WINPID was 239116).

Consequences on the affected path:

1. **Liveness inverts.** `pidutil.Alive(msysPid)` is almost always false, so gc
   concludes managed dolt is dead while it is running — the same *symptom* class
   as gw‑1oa, from the opposite cause (there the *probe* was wrong; here the
   *value* is).
2. **Collateral kill.** `dolt_stop_managed.go:125/150` calls `platformKill`,
   which on Windows runs `taskkill /T [/F] /PID <pid>`. If the MSYS number
   collides with a live Windows pid, `gc dolt stop` terminates **an unrelated
   process on the user's machine** and the real server survives. Observed MSYS
   values (1749, 10782, 46077) sit squarely in Windows' assignment range, so
   collision is ordinary, not exotic.

## 3. Boundary inventory (the facts that shape the fix)

**Pid capture in pack scripts — 3 sites, only 1 crosses the boundary:**

| Site | Use | Crosses to Go? |
| --- | --- | --- |
| `gc-beads-bd.sh:207` `cmd_pid=$!` | `run_with_timeout` internal | No |
| `gc-beads-bd.sh:212` `watchdog_pid=$!` | watchdog internal | No |
| `gc-beads-bd.sh:2400` `server_pid=$!` | → `$PID_FILE` **and** `save_state` → `dolt-state.json` | **Yes** |

This matters: the rule is not "never use `$!`". In‑script pids may stay in MSYS
space because only sh touches them. Exactly **one** value needs conversion.

**sh-side operations on a stored pid — 6 sites:** `:275` (inside `pid_alive`),
`:1136`, `:1149`, `:1227`, `:1249`, `:2439`. These call MSYS `kill`, which will
*not* accept a native pid. Converting the stored value without converting these
yields a half‑converted pid space — worse than a consistently wrong one.

**Go readers:** `beads_provider_lifecycle.go:1490/1519/1530`,
`dolt_stop_managed.go:125/150/225`.

**Scope — the shell fallback only.** When `GC_BIN` is set the script delegates to
`gc dolt-state start-managed` (script `:1608`) and the Go helper records a real
native pid (`dolt_start_managed.go:451/549`). The `nohup … &` branch at `:2400`
runs when `GC_BIN` is empty. That is **by design, not only in tests**: three Go
paths deliberately strip `GC_BIN` from the child env (`bd_env.go:790`,
`beads_provider_lifecycle.go:2216`, `cmd_commands.go:347`). So the fallback
cannot simply be deleted.

## 4. Why this happened

Not a coding slip. gascity already has a family of Tier‑1/Tier‑2 boundary
translation rules — P1/P2 exec, P3/P4/P8 paths, P5 mode bits, P7 `/proc`
equivalents, P9 tree teardown. Every one names a representation that crosses the
boundary and the mechanism that translates it.

**No rule was ever written for process identity.** The pid field in
`dolt-state.json` is an untyped integer on a cross‑tier wire. That is the gap.

## 5. Consistency audit

**Consistent with the two-tier doctrine? Yes — and it dictates the answer.**
The doctrine says Tier‑1 SDK mechanisms are Windows‑*native* (`pidutil`,
`winjob`, native probes) and must "never emulate /proc, lsof, chmod". An MSYS pid
is an artifact of the Tier‑2 interpreter. Letting it into Tier‑1 state is exactly
the emulation the doctrine forbids. **The boundary currency must be the native OS
pid.**

**Consistent with the P1–P10 family? It is the missing member.** The fix should
look like its siblings: a named class, one shared helper per side, a CI guard.

**Inconsistent with a settled principle — and this is the deeper problem.**
`AGENTS.md:336`:

> **No status files — query live state.** Never write PID files, lock files, or
> state files to track running processes. … The process table is the single
> source of truth for "what is running."

`$PID_FILE` and the `pid` field in `dolt-state.json` are precisely what that
forbids. The pid‑space bug is a *symptom* of a mechanism that should not exist.
Any plan that only fixes the numbering space leaves the doctrine violation in
place.

## 6. Options

**A. Native pid is the boundary currency; sh converts at capture.**
Add `native_pid_of()` (map via `ps -W`) and a `pid_kill()` companion to the
existing `pid_alive()`. Store native pids; route all 6 sh kill sites through
`pid_kill`.
*Pro:* Tier‑1 unchanged and stays pure‑native; one conversion point; composes
with the `pid_alive()` added for gw‑1oa.
*Con:* every sh liveness check on a native pid falls through `kill -0` to
`ps -W`, which enumerates all processes — and `pid_alive` is called in polling
loops (`:1228`, `:1239`, 16 call sites total). Needs a Windows‑first ordering or
delegation, or start‑up polling gets measurably slower.

**B. MSYS pid is the currency; Go converts on read.** *Rejected.* Tier‑1 would
have to shell out to the Tier‑2 interpreter to understand a value it stored, and
the mapping evaporates when the process dies — so Go could not even interpret a
stale pid. Directly contradicts the doctrine.

**C. Tier‑1 owns process lifecycle; scripts never spawn daemons.**
Make the `gc dolt-state start-managed` delegation mandatory and fail loudly
without it.
*Pro:* eliminates the class rather than translating it; matches "the controller
drives all SDK infrastructure operations".
*Con:* blocked as stated — `GC_BIN` is deliberately stripped on three paths, so
those flows would need a guaranteed gc binary first. Also, pack portability means
a pack author can still spawn something, so the boundary contract (A) is still
needed as the backstop.

**D. Store no pid at all — discover by port + process table.**
The doctrine‑pure answer, and what `AGENTS.md:336` actually asks for.
*Pro:* removes the stale‑state class entirely, not just its Windows symptom.
*Con:* largest change; `dolt-state.json` is load‑bearing today; needs a
platform‑neutral identity story (port holder + argv match, which `pidutil` and
the doctor's `GetExtendedTcpTable` arm already provide).

## 7. Recommendation

Phased, because A is small and correct while D is right and large.

- **Phase 1 (now, P1): make the boundary contract explicit and correct — Option A.**
  Convert at capture; add `pid_kill()`; route the 6 sh sites; keep Tier‑1
  untouched. Resolve the `ps -W` cost with a Windows‑first ordering in
  `pid_alive` (try the native match first rather than after a `kill -0` that is
  known to fail for native pids).
- **Phase 2: close the door — Option C where cheap.** Audit the three `GC_BIN`
  strip sites; where a gc binary is guaranteed, make delegation mandatory so the
  fallback stops being reachable in normal operation.
- **Phase 3: retire the pid field — Option D.** Move managed‑dolt discovery to
  port‑holder + argv identity and delete `$PID_FILE` / `state.pid`, satisfying
  `AGENTS.md:336`.

Phase 1 alone removes the collateral‑kill hazard, which is the part that can
damage a user's machine.

## 8. Maintainability — why prose will not hold this

The strongest evidence is in our own doctrine. **T12 already documents the
"no `/tmp` on the runner" rule**, and that class still recurred **three times**
this month (dolt‑preflight socket, tmux socket parent, tmux socket root). A
documented rule with no mechanism did not survive contact.

Compare the classes that *do* hold: P1/P2 (execshim), P5 (winsec), P7 (pidutil),
P9 (testenv + winjob). Each is enforced by a **shared helper you must call** or a
**CI check**, not by remembering a table.

So Phase 1 is incomplete without:

1. **One mechanism per side.** sh: `native_pid_of` / `pid_alive` / `pid_kill` as
   the only sanctioned pid operations. Go: `pidutil` only — already true.
2. **A CI guard.** A lint that fails when a pack script writes `$!`/`$$` into a
   persisted file (state file, pid file) without conversion. Cheap, in the style
   of `scripts/check-core-boundary.sh` and `TestOpenAPISpecInSync`.
3. **One real regression test.** Start managed dolt through the shell fallback
   (`GC_BIN` unset) and assert `pidutil.Alive(state.PID)` — i.e. that the two
   tiers agree on the pid space. **That assertion fails today**, which is what
   makes it worth writing.
4. **A new doctrine class P11** ("process identity crossing the tier boundary"),
   pointing at the helpers, so the table stays a complete index of the family.

Also fix the test‑side instance under gw‑drq:
`processgrouptest.KillFromPIDFile` reads a shell‑written pid and calls `Kill()`
with no verification, so on Windows it can terminate an arbitrary matching
process on a developer's machine.

## 8a. Experiment: is `state.pid` derivable? (RUN — answered)

Question 1 of section 9 was settled empirically rather than by argument. A real TCP
listener was held in a known process, a `dolt-state.json` written against it, and the
production predicates called directly:

```
MEASURE 1  findPortHolderPID(49218) = 86564 ; os.Getpid() = 86564 ; match=true
MEASURE 2  validDoltRuntimeState(correct pid) = true
           inspectManagedDoltOwnership(pid) owned=false deleted=false   (our proc is not dolt)
           managedDoltRuntimeProcessOwned    = true
MEASURE 3  identity ok with PID=0 = false  (the PID<=0 guard)
           but findPortHolderPID still returns 86564 — the value is recoverable
MEASURE 4  validDoltRuntimeState(derived pid=86564) = true
```

**Findings.**

1. **The pid is recoverable from the port.** Native attribution
   (`GetExtendedTcpTable` via `pidutil.TCPListenerPID`) returns the exact pid on
   Windows (M1). Substituting it validates identically (M4).
2. **The `PID <= 0` reject is a guard, not a need** (M3).
3. **CORRECTION to section 7 — "just pass `holderPID`" is wrong.** M2 shows
   `managedDoltRuntimeProcessOwned` returned **true while `owned` was false**: it
   passed purely through the `holderPID == state.PID` branch, with ownership
   inspection contributing nothing. So `state.PID`'s real function is *the
   remembered identity the discovered holder must match*. Deriving the pid from the
   port makes that comparison `holderPID == holderPID` — **tautologically true**,
   i.e. exactly the class of vacuous check this session has been removing
   elsewhere. It would silently accept any process holding the port.

**So Phase 3 is feasible but must be stated correctly:** replace *remembered pid*
with *verified identity* — make `inspectManagedDoltOwnership` authoritative and drop
the pid-equality shortcut. M2 is the encouraging part: ownership inspection already
discriminates correctly (`owned=false` for a non-dolt port holder), so the
authority already exists; today it is merely short-circuited.

**Footnote (latent, pre-existing, not Windows-specific).** Because the pid-equality
branch wins before ownership is consulted, a recycled pid that coincides with the
port holder would be accepted as managed dolt without ever verifying it *is* dolt.
Vanishingly unlikely, but it is the same short-circuit, so making ownership
authoritative fixes it too.

## 8b. Questions 2 and 3: reviewed (both change the plan)

### Q2 — why is `GC_BIN` stripped? CORRECTION: it is not.

I previously wrote that three Go paths "deliberately strip `GC_BIN`, so the fallback
is by design". That was a misreading. All three are **remove-then-set** idioms that
de-duplicate before pinning:

```go
if gcBin := resolveProviderLifecycleGCBinary(); gcBin != "" {
    environ = removeEnvKey(environ, "GC_BIN")   // de-dup
    environ = append(environ, "GC_BIN="+gcBin)  // then pin
}
```

`GC_BIN` is empty only when the resolver returns "" (`beads_provider_lifecycle.go:133`):

```go
if isTestBinary() { return "" }                                  // fork-bomb guard, P6 / gw-8g5
if exe, err := os.Executable(); err == nil && exe != "" { return exe }
if path, err := execshim.LookPath("gc"); err == nil && path != "" { return path }
return ""
```

So the shell-spawn fallback is reached when:

1. **Inside a Go test binary** — deliberate: setting `GC_BIN` to a test executable is
   what fork-bombed the host (incident gw-8g5, 4500 procs / 246GB). This is the
   common trigger.
2. `os.Executable()` fails *and* no `gc` on PATH — effectively never.
3. **The script is invoked without `GC_BIN` in its env** — by hand, or by any caller
   that is not gc's own env plumbing.

**Reachability correction for gw-dbm.** Production operation through gc almost always
delegates, so the collateral-kill hazard is *not* a routine user-facing path. It is
routine in **test runs on developer machines and CI**, and reachable by (3). Severity
should be restated on that basis rather than as "users lose processes".

**Maintainability smell worth fixing cheaply.** The start delegation uses
`resolve_gc_helper_bin` (line 81) which honours **only** `GC_BIN`, while
`resolve_gc_bin` (line 92) also falls back to `command -v gc`. So a hand-run script
skips delegation even when `gc` is on PATH. Pointing the start path at the
PATH-aware resolver shrinks the fallback to "no gc installed at all" — a one-line
Phase-2 win.

### Q3 — cost of `ps -W` per liveness check: MEASURED, and it rules out Option A as written

```
ps -W   x20  total_ms=11216   per_call_ms=560
kill -0 x20  total_ms=138     (~7ms per call)
```

`ps -W` is **~80x** the cost of `kill -0`. `pid_alive` has 16 call sites including
polling loops at `:1228` (60 iterations) and `:1239`. Storing a native pid would push
every one of those through the `ps -W` fallback: **60 x 560ms ~= 34 seconds of added
overhead** in a loop whose whole budget is 30s. That is a functional regression, not a
micro-optimisation.

**Revised Phase 1 — name the space instead of converting on read.** Persist both
identities, each labelled, and pay the mapping **once at spawn** instead of once per
poll:

| Field | Space | Consumer |
| --- | --- | --- |
| `pid` | native OS pid | Go / Tier 1 (`pidutil`, `platformKill`) |
| `shell_pid` | interpreter pid (MSYS on Windows; identical to `pid` on Unix) | sh / Tier 2 (`kill`, `kill -0`) |

Properties: one `ps -W` per daemon start (~560ms, acceptable); sh keeps its cheap
`kill -0` in every loop, so **no polling regression**; the field name declares the
numbering space, which is the "typed wire" discipline the repo already applies to
HTTP/SSE payloads; and Go needs no change. It also degenerates cleanly on Unix, where
the two fields are equal by construction.

**Known asymmetry to cover in tests.** When the Go helper spawns (GC_BIN set) it
writes a native pid; when sh stops without `GC_BIN` it would `kill` that native pid,
which MSYS `kill` rejects. Both stop paths currently delegate symmetrically
(`:1722`), so this does not bite today — but it is exactly the mixed-origin case a
test should pin.

## 9. Open questions for the human

1. ~~**Phase 3 appetite.**~~ **ANSWERED by the experiment in 8a** — the pid is
   derivable, so Phase 3 is feasible, but it requires making ownership inspection
   authoritative rather than simply substituting `holderPID`. Note the file itself
   should probably stay regardless: `cmd_dolt_state.go:517-525` prints
   `Running`/`DataDir`/`StartedAt` for operators, so Phase 3 retires the *pid
   field*, not `dolt-state.json`. That makes Phase 1 a durable boundary contract
   rather than scaffolding.
2. **`GC_BIN` stripping.** Those three sites strip it deliberately (recursion
   guard?). Is a guaranteed gc binary available on those paths, or is the shell
   fallback genuinely load‑bearing?
3. **Polling cost.** Is a `ps -W` per liveness check acceptable during a 30 s
   readiness poll, or should sh delegate liveness to `gc` (as it already does for
   `wait-ready`) whenever `GC_BIN` exists?

## 10. Acceptance criteria

- Phase 1: the fallback‑start regression test passes; all 6 sh kill sites go
  through `pid_kill`; the lint rejects a raw `$!` written to a persisted file;
  doctrine table gains P11; managed dolt start/stop suites stay green on the
  Windows gate.
- Phase 2: no normal‑operation path reaches the `nohup … &` branch; a test
  asserts delegation is used when `GC_BIN` is present.
- Phase 3: `$PID_FILE` and `state.pid` are gone; discovery is port+argv;
  `AGENTS.md:336` holds for managed dolt.

## 11. Test plan — one suite, both platforms, no skips

The failure this whole thread came from was a test that **passed while asserting
nothing**. So the design rule for this suite is: *express the contract, not the
mechanism*, so the same assertion is meaningful on Linux and Windows and cannot be
satisfied by accident.

Concretely — never assert "the stored value is a WINPID" (that bakes in `ps -W` and
is unrunnable on Linux). Assert **"the pid Go reads is accepted by the same probe
production uses"**. That sentence is true-by-construction on Unix and false today on
Windows, which is exactly what a contract test should look like.

### The suite

| # | Test | Asserts | Today |
| --- | --- | --- | --- |
| 1 | `TestManagedDoltStatePIDIsNativeAfterShellFallbackStart` | after a `GC_BIN`-unset start, `pidutil.Alive(state.PID)` is true | **FAILS on Windows** (encodes the bug) |
| 2 | `TestManagedDoltStatePIDIsNativeAfterHelperStart` | same invariant with `GC_BIN` set (Go spawns) | passes — locks the good path |
| 3 | `TestShellPIDFieldIsRejectedAsNativePID` | a state file carrying a *shell*-space pid is judged invalid | guards against "fix by relabelling" |
| 4 | `TestPidKillTerminatesStoredProcess` | `pid_kill` on the persisted identity actually terminates it | **FAILS on Windows** pre-fix |
| 5 | `TestPidAliveAgreesWithNativeProbeForBothSpaces` | sh `pid_alive(shell_pid)` and `pidutil.Alive(pid)` agree about the same process, alive and dead | round-trips the boundary |
| 6 | `TestMixedOriginStopKillsHelperStartedServer` | Go-spawned server stopped through the sh path (the Q3 asymmetry) | pins the untested corner |
| 7 | `TestOwnershipIsAuthoritativeNotPidEquality` | a non-dolt process holding the port is **rejected** | **FAILS today** (see 8a MEASURE 2); Phase 3 gate |
| 8 | `TestPackScriptsDoNotPersistShellPIDs` (lint) | no pack script writes `$!`/`$$` into a persisted file without conversion | authoring-time guard |

### Why each earns its place

- **1 + 2 together** are the real contract: the invariant must hold *regardless of who
  spawned the daemon*. Testing only the fallback would let a future refactor move the
  bug into the helper path unnoticed.
- **3** is the anti-vacuity test. Without it, someone could "satisfy" test 1 by writing
  the shell pid into the native field and renaming nothing.
- **4** matters because liveness and termination are separate failure modes: gw-1oa was
  a broken *probe*, gw-dbm is a broken *value*, and a wrong kill target is a third
  thing. A liveness-only suite would miss it.
- **5** is the round-trip that makes the two-field design honest — both fields must
  describe the *same* process, and both must flip to dead together.
- **7** is written now but expected to fail until Phase 3; it encodes the corrected
  design from 8a rather than leaving it as prose.
- **8** exists because prose did not hold T12 (three recurrences). Every doctrine class
  that actually survives in this repo is backed by a helper you must call or a CI
  check.

### Platform mechanics (so none of these get skipped)

- Use a **real** process on both platforms — a held TCP listener plus its recorded
  identity — not a fabricated pid. Fabricated pids are what made the earlier fixtures
  vacuous.
- Map shell→native space **only inside the fixture helper**, never in the assertion, so
  the assertion text stays platform-neutral.
- Do the mapping while the process is **alive**: `ps -W` only lists live processes, so
  a test that asserts death must capture identity first (learned the hard way in
  `waitForProviderTestChildPID`).
- On Unix the two fields are equal by construction; the tests still run and still guard
  against a regression that diverges them.
- Tests 1/2/4/6 need a spawnable daemon. Where `dolt` is unavailable (the GitHub
  runner has no `dolt.exe` — doctrine T12), substitute a long-lived listener via the
  existing fake-tool pattern rather than skipping: the contract under test is about
  pid bookkeeping, not about dolt.

### Acceptance

Phase 1 lands when 1, 2, 3, 4, 5, 6 and 8 are green on the Windows gate **and** Linux,
with 7 still red and referenced from the Phase 3 bead. Every one of 1, 4 and 7 must be
demonstrated failing before its fix, for the reason this document exists.

## 12. Implementation findings (Phase 1, in progress)

**The contract test is written and DEMONSTRATED FAILING** through the real start path
(`TestManagedDoltStatePIDIsNativeAfterShellFallbackStart`):

```
dolt-state.json pid 49126 is not live per pidutil.Alive, the probe production uses.
The script recorded a pid in a different numbering space than Go reads it in (gw-dbm)
```

Three fixture facts worth keeping, each cost a debugging cycle:

1. The fake `dolt` must be **subcommand-aware**. Start also runs
   `dolt config --global --get user.*`, `dolt version`, and — with `GC_BIN` unset — a
   `dolt --host … sql` query probe. A blanket long-lived fake blocks identity setup
   rather than standing in for the server.
2. This path **cannot** use the prelude-override harness the other script tests use:
   the env→globals initialisation (`DATA_DIR`, `LOCK_FILE`, …) lives in the script's
   **Main** section, so `op_start` against the prelude alone leaves them empty and the
   script dies in `mkdir -p '' .`.
3. Readiness is satisfied by binding the port from Go **after** the spawn. Binding
   first makes the script treat the port as already-served and adopt instead of spawn,
   which is not the path under test.

**Test tier:** it lives behind `skipSlowChoiceCmdGCTest`-style gating because it spawns
processes, and the Windows gate sets `GC_FAST_UNIT=1`. So this contract is enforced by
`make test-cmd-gc-process`, **not** by the gate. State that plainly rather than
assuming gate-green implies the contract holds.

### The finding that changes the wire design

**Go reads the PID FILE as well as the state file** —
`dolt_process_inspection.go:53/68` `managedPIDFromPIDFile(layout.PIDFile)`. So *both*
`$PID_FILE` and `state.pid` cross the boundary and both must be native.

That has a consequence the earlier plan missed. sh's own polling reads a pid too:
`graceful_stop_owned_pid` (script `:1224`) polls `pid_alive` in a 60-iteration loop and
its callers pass pids sourced from the persisted files (`:2254`, `:2293`, `:2310`,
`:3200`). If those files hold *native* pids, every one of those polls falls through to
`ps -W` at 560ms — reintroducing exactly the ~34s regression the two-field design was
meant to avoid.

**So the two-field design must be stated more strongly:** it is not enough to add
`shell_pid` alongside a native `pid`. sh must be **rerouted to read `shell_pid`** for
its own liveness/kill work, and the native `pid`/`$PID_FILE` becomes Go-only. Until
that rerouting is complete the space is half-converted, which this document already
argues is worse than consistently wrong.

Concretely, Phase 1 is:

1. `native_pid_of()` in the script (one `ps -W` at spawn).
2. `save_state` emits `pid` (native) **and** `shell_pid`; `$PID_FILE` becomes native.
3. Go's `doltRuntimeState` gains `ShellPID` (read-only; Go never uses it).
4. **Reroute** the four `graceful_stop_owned_pid` call sites and the `:1820`
   `$PID_FILE` read to the shell-space value, so no sh polling loop touches a native
   pid.
5. `pid_kill()` for the cases where sh must terminate a native pid anyway.

Step 4 is the load-bearing one and the reason Phase 1 was not landed in the same
session as the test: getting it half-right is the documented worse outcome.

## 13. RETRACTION: the two-field design is wrong; unify on native

Section 8b proposed persisting both `pid` (native) and `shell_pid` (interpreter) to
avoid a `ps -W` cost in sh's polling loops. Further tracing shows that reasoning was
built on a false premise.

**The script already receives and polls NATIVE pids.** `GC_PROBE_PORT_HOLDER_PID`
(script `:829`) and `GC_EXISTING_MANAGED_PID` (`:881`) are parsed out of
`gc dolt-state probe-managed` / `existing-managed` — i.e. produced by Go, in native
space. Those values are passed straight to `graceful_stop_owned_pid` (`:2254`, `:2293`,
`:2310`), whose loop calls `pid_alive` up to 60 times.

Two consequences:

1. **The mixing is not hypothetical — it exists today.** Adding `shell_pid` would
   institutionalise a second space rather than remove the confusion.
2. **The `ps -W` cost is already being paid.** For any gc-helper-supplied pid the
   `kill -0` fast path fails and every poll falls through to `ps -W`. A 30s graceful
   stop already takes ~63s of wall clock on Windows (60 x (500ms sleep + 560ms probe)).
   That is a pre-existing degradation, not something Option A would introduce.

`tasklist //FI "PID eq N" //NH` was measured as an alternative: it works (the `//FI`
escape defeats MSYS argument mangling) but costs **1371ms**, worse than `ps -W`.
There is no cheap native liveness probe available to sh.

### Revised Phase 1

1. `native_pid_of()` — map once at capture (`ps -W`, identity off Windows).
2. Persist **native** in both `$PID_FILE` and `state.pid`. One space crosses the
   boundary; no new field; Go unchanged.
3. `pid_kill()` — MSYS `kill` first (correct for interpreter pids), `taskkill //T`
   fallback for native ones. Symmetric with `pid_alive`, and required because MSYS
   `kill` cannot terminate a native pid.
4. Make the stop/wait loops **deadline-based** rather than iteration-counted, so an
   expensive probe costs samples instead of stretching the budget. This fixes the
   pre-existing 30s→63s overshoot and needs no platform special-casing.

Simpler than the two-field scheme, removes the existing mixing instead of encoding it,
and corrects a latent timing bug on the way through.
