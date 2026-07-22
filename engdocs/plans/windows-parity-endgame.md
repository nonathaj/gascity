# Windows Parity Endgame Plan

| Field | Value |
|---|---|
| Status | Proposed |
| Date | 2026-07-21 |
| Umbrella bead | gw-xwj |
| Doctrine | `engdocs/contributors/windows-portability.md` |
| Feature spec | `engdocs/design/windows-systemd-parity.md` |

## Where we are

157 packages have tests; **134 are green-listed** and gated in Windows
CI. Fresh canary data (2026-07-21, contained runs):

- **3 packages are already green, just unlisted**:
  `internal/runtime/exec`, `internal/testenv`, `cmd/gen-command-census`.
- **13 packages have ~35 failures total**, almost all recognizable
  doctrine classes (inventory in Tranche C below).
- **7 packages were deliberately not canaried**: `cmd/gc` (the
  campaign), `scripts` (gw-f2w), the dolt-spawning trio
  (`examples/bd/dolt`, `examples/gastown`, `test/dolttest`), and the
  infra-gated `internal/runtime/{k8s,t3bridge}`.

The pattern that held for every tranche so far — *each dark package
contains at least one real production bug* — is the reason to finish
this rather than gate-and-forget.

## Tranche R0 — make the gate real (before everything)

Review finding (2026-07-21): `windows-gate.yml` triggered only on
`main` and the long-deleted `windows-port` branch — **no commit on the
parity branch has ever run in CI**, so "134 green" was a dev-box
claim, not a CI fact. Steps:

1. Fix triggers: `main` + `fix/**` pushes (done alongside this plan
   revision), keep `workflow_dispatch`.
2. Let the trigger-fix push run the gate on this branch; triage
   runner-vs-devbox deltas. Predictable ones: **no `dolt.exe` on
   `windows-latest`** (dolt-dependent tests must self-skip when the
   binary is absent — locally unverifiable because dolt is always
   installed here), different Git-for-Windows footprint, no
   `%GOPATH%\bin` tools.
3. Adopt the policy: a package is only "green" once it passes **on
   the runner**, and green-listing any package >~30 tests requires
   two consecutive clean full runs (single `count=1` passes have
   already lied — the FormulaFeed TTL flake).

## Tranche R — consolidation refactors (first; small; locks in doctrine)

Debt accumulated while moving fast. Doing these first prevents churn in
files the cmd/gc tranche will touch, and turns three copy-paste
patterns into named primitives.

1. **`pathutil` file-URL helpers.** Three hand-rolled copies of the
   drive-letter `file://` parse (doctor backup state, packregistry,
   plus test-side builders) become
   `pathutil.LocalPathFromFileURL(*url.URL) (string, error)` and
   `pathutil.FileURLForLocalPath(string) string`, with the doctrine-P3
   cases as table tests. Sweep the three sites.
2. **`testutil.MakeFileUnopenable`.** The exclusive-no-share-handle
   error injection exists twice (sessionlog, eventexport); move to
   testutil (`_windows`/`_unix` pair), consumers keep one-line calls.
3. **De-duplicate the PEB reader.** `cmd/gc/dolt_process_discovery_windows.go`
   carries the original copy of what is now `pidutil.Cmdline`; the
   discovery loop should consume pidutil (it wants argv anyway).
4. **Shared TCP port→PID.** Move doctor's `GetExtendedTcpTable` arm to
   `pidutil.TCPListenerPID(port) (int, bool)`; wire doctor and dolt
   discovery's `portsByPID` (whose "yields nothing on Windows" TODO
   this closes — Windows dolt cleanup gains real port attribution).

5. **Doctrine lints (the maintainability keystone).** The doctrine is
   prose; this repo enforces its invariants with lint-tests (testenv
   import lint, env-baseline golden, worker-boundary import test).
   Add a policy test in the same family that pattern-scans non-test
   production sources for doctrine violations: bare `".test"` suffix
   checks outside execshim (P6 — this exact miss fork-bombed the
   host), raw `exec.LookPath`/`exec.Command` in packages already
   swept to execshim (allowlist-based, like the worker boundary), and
   `t.Setenv("HOME", …)` without a paired `USERPROFILE` in test files
   (T1). This turns Linux/Windows co-maintenance from review-dependent
   into self-policing.

Rules for this tranche: R touches already-green packages (doctor,
packregistry, sessionlog, eventexport) — every touched package reruns
its full gate. `pidutil.TCPListenerPID` placement is a recorded
decision: pid-centric lookup belongs in pidutil; a new one-function
`netutil` package was rejected (KISS). When R1 lands, update the
doctrine's P3 row to point at the pathutil helpers.

Exit: all five landed with tests; no behavior change on Linux
(cross-vet + affected-package runs).

## Tranche C — the 13 small packages (+3 free listings)

Class-mapped from canary output; work them in this order (shared-cause
first):

| Package | Fails | Diagnosis (doctrine class) |
|---|---|---|
| `internal/supervisor` | 13 | `TestDefaultHomeWithEnv` shape → T1 (HOME/USERPROFILE), likely one fixture helper |
| `internal/beads` | 5 | `TestBdStorePurge` cluster — triage; likely T2/P4 paths |
| `internal/workspacesvc` | 4 | proxy-process reload/publication env — triage; P1/P2 candidates |
| `internal/worker/workertest` | 3 | Phase-1 conformance — likely P1 (provider script exec) |
| `internal/docgen` | 3 | Known: `invopop/jsonschema.AddGoComments` builds backslash comment-map keys. Fix **without forking**: post-process the CommentMap, `ToSlash`-ing keys, behind a small wrapper; pin with a golden |
| `test/docsync` | 2 | Schema freshness — expected to fall out of the docgen fix; verify |
| `internal/productmetrics` | 2 | "pins HOME" → T1 |
| `test/acceptance/helpers` | 2 | binary override path — T2/T3; note the harness itself is Unix-shaped (sh shims), so scope is its unit tests only |
| `internal/clientcontext` | 1 | owner-only perms → P5 (not-applicable arm or T-gate; decide per production semantics like pgauth) |
| `internal/dispatch` | 1 | store-path relative check → P4 |
| `internal/worker` | 1 | transcript search paths → T2 |
| `internal/worker/transcript` | 1 | Kimi session-key discovery → T9 (verify against kimi-cli source before pinning) |
| `internal/testpolicy/resourcecensus` | 1 | repo ledger census — likely T10 (Rel/ToSlash in the census walk) |

Rules of engagement: fix production over gating wherever the platform
has real semantics; every skip names its mechanism; each package lands
as its own commit with green-list addition; full-package rerun plus
zero-leftover check every time.

Also in this tranche (parked-bead integration from the plan review):

- **gw-ho3** — exec-provider watch-startup teardown orphans an sh
  grandchild (live production Windows bug, doctrine P9). Production
  item; land before or with the workspacesvc/worker packages it
  touches.
- **gw-3ic** — fsys rename-over-open + RemoveAll retry hardening
  (production; rotation and worktree/pack cleanup paths).
- **gw-l8d** — split the 7 test files using Unix-only syscalls behind
  build tags, then upgrade the gate's cross-compile job to
  `GOOS=windows go vet` (the gate header documents this intent) —
  catches `_test.go` compile regressions no CI currently sees.
- **gw-e0a** (conformance fixture slimming) and **gw-a8g** (proctable
  nits) ride along where their packages are touched.
- **gw-rdd** is stale (api and doctor are done) — close it.

Exit: all 16 green-listed (150/157) **on the runner**, plus the
parked production items above.

## Tranche A — the `cmd/gc` campaign

**Scale: 7,844 test functions, 499 test files, 32 testscript txtar
suites.** This is ~8× `internal/api` and gets a campaign structure, not
a single pass.

### A0. CI-budget guardrail (before adding to the gate)

`cmd/gc` is minutes-long even healthy. Before green-listing, measure a
full contained run; if it pushes the windows-gate job past ~15 min,
split the gate into two shards (the green-list file format already
supports splitting by prefix; add a second matrix entry in
`windows-gate.yml`). Decide with data, not vibes.

### A1. Production seams sweep (repo-wide, exemption-listed)

The 63 remaining non-test `exec.LookPath` sites (mostly `cmd/gc`)
switch to `execshim.LookPath` **by default**, with a short, commented
exemption list for sites where host-PATH-only is the semantic:

- `delegatedSystemctlPath` / host-dir guards (systemd delegation is
  Linux-only and panics on host resolution in tests — leave).
- Doctor's `lsof`/`pgrep` binary checks: these gate Unix-only
  mechanisms; instead of sweeping, give the checks not-applicable arms
  on Windows (P5-adjacent), mirroring the secrets-perms decision.

Concrete non-test site inventory (snapshot 2026-07-21; `internal/*`
green-listed sites already swept and lint-guarded):

- **Sweep to `execshim.LookPath` — `LookPathFunc` injected into a
  resolver** (provider/agent-binary resolution needs the coreutils +
  PATHEXT fallback): `cmd/gc/adoption_barrier.go:361`,
  `agent_build_params.go:117`, `city_runtime.go:2802`,
  `cmd_doctor.go:{248,249}` (MCP checks), `cmd_internal_project_mcp.go:{57,82}`,
  `cmd_mcp.go:{82,95}`, `cmd_nudge.go:1240`, `cmd_prime.go:306`,
  `cmd_prompt.go:229`, `cmd_session.go:{194,264,1605,1618}`,
  `cmd_sling.go:1514`, `cmd_start.go:794`, `cmd_supervisor.go:2607`,
  `session_template_start.go:{117,277}`, `worker_handle.go:{865,875}`,
  `init_provider_readiness.go:511` (`var initLookPath`),
  `internal/doctor/checks.go:{381,389}`,
  `internal/doctor/checks_rig_root_branch.go:23`.
- **Sweep — direct guard for a tool that exists on Windows**:
  `cmd/gc/beads_provider_lifecycle.go:137` (`gc`), `cmd_bd.go:298` (`bd`),
  `main.go:1384` (`bd`), `doctor_pack_runtimes.go:81` (pack `rt.Command`),
  `cmd_doctor.go:{275,276,277}` (`tmux`/`git`/`jq` via `NewBinaryCheck` —
  `tmux` legitimately absent → the check reports missing, which is correct).
- **Not-applicable arm on Windows (do NOT route to execshim; these tools
  don't exist there)**: `cmd_doctor.go:{289,290}` (`pgrep`/`lsof`),
  `dolt_cleanup_discovery.go:662`, `dolt_preflight_cleanup.go:96`,
  `dolt_process_inspection.go:{90,240,307}` (all `lsof`; the Windows
  port replaces lsof-based port attribution with `pidutil` GetExtendedTcpTable).
- **Exempt (systemd, host-PATH-only; winportlint-allowlisted)**:
  `cmd/gc/supervisor_systemd_delegate.go:98` (`systemctl`),
  `internal/runtime/tmux/agent_slice.go:63` (`systemd-run`).

Also in this phase: **gw-nsg** — the curated provider-probe base PATH
gets a per-GOOS default. Windows list (computed lazily from env, never
at init): `%SystemRoot%\System32` (cmd.exe runs `.cmd` shims),
`%APPDATA%\npm` (npm global shims — where `claude.cmd` lives),
`%LOCALAPPDATA%\Programs` subdirs, `%ProgramFiles%\GitHub CLI`.
Acceptance is empirical: the probe must find the real `claude` and
`gh` installed on the Windows port box.

And the known sh-quoting bug: `shellWorkQuery` embeds the gc binary
path in sh text where backslashes are eaten — `ToSlash` before
quoting (doctrine P8). This fixes `TestProductMetricsDirectChildEnvHookWorkQuery`
and, more importantly, every real hook work-query on Windows.

Known landmine, pre-registered: cmd/gc's `TestMain` isolates its temp
root by setting **only `TMPDIR`**, which `os.TempDir()` ignores on
Windows (it reads `TMP`/`TEMP`) — so the package's temp isolation and
orphan sweeps are partially void there. Expect this to explain a
multi-failure cluster; fix is the established `setTestTempDir` class
(set all three).

### A2. Suite-sharded inventory

One contained full run, failures bucketed **by test file** (the suite
boundaries in cmd/gc are file-shaped: supervisor, dolt, nudge, sling,
session, init, doctor, testscript driver). Store the bucketed
inventory on the tranche bead. Expectation from sampling: a large
fraction cascades from A1; the rest are doctrine classes.

### A3. Class application, suite by suite

Work suites largest-first, one commit per suite (or per class when a
class spans suites). Known deliberate gates, marked with dissolving
beads:

- systemd/launchd delegate + install + sentinel tests → Linux
  mechanism; gates reference **gw-x1k** (Task Scheduler arm adds
  Windows-native equivalents rather than un-gating these).
- `/proc`-sweep workspace cleanup tests → **gw-8su**.

### A4. testscript txtar strategy

The 32 txtars exec the real `gc` binary. Windows failure modes and
their canonical handling:

1. **Path-form assertions in golden output** → the rule is sharper
   than "prefer slash-form": *identifiers* (scope refs,
   config-authored paths, slugs) are canonical slash-form on every
   platform per P4; *user-facing filesystem paths* stay native —
   that is correct Windows UX, and their goldens fork via
   testscript's built-in `[windows]`/`[!windows]` conditions inside
   the txtar rather than duplicated files. Never normalize a real
   path just to avoid a golden fork.
2. **sh-dependent script steps** → testscript's `exec` runs through
   the harness; steps invoking coreutils get the same execshim
   treatment gc itself uses, or `[!windows]` gating when the step
   tests a Unix mechanism.
3. **CRLF** → txtar files join the `.gitattributes` LF pin.

Budget one focused session for the txtar corpus alone; it is the
least-predictable part of the campaign.

### Exit

`cmd/gc` green-listed (with the documented delegate/proc gates), gate
runtime within budget, and the two production items (gw-nsg, work-query
quoting) verified against the real CLIs on the port box.

## Tranche B — `scripts` and the Windows developer workflow (gw-f2w)

The remaining `./scripts` failures test the **bash harness itself**
(`go-test-observable` logging, make-isolation env crossing, gomod
replace guard). Decision to make explicitly rather than test-by-test:

> **The sanctioned Windows dev workflow is direct `go test` (plus the
> documented shard filters).** Containment is in-binary (automatic),
> so the bash entrypoints' Windows story is "not used there" — the
> harness stays Unix, and its self-tests gate on Windows with a
> pointer to this decision.

Concretely: gate the three harness self-tests with mechanism-naming
skips; add a "Windows developer workflow" subsection to TESTING.md
(direct `go test`, the containment guarantee, `GC_TEST_*` knobs);
green-list `./scripts`. The gomod-replace guard is CI-side (Linux) —
if we ever want it cross-platform it routes through
`execshim.ShellCommand`, but that is not this tranche.

Accepted deltas, stated explicitly: Windows developers lose the
observable-run logging, the timing census, and slice memory budgeting
that the bash entrypoints provide on Linux — containment (the safety
property) is platform-neutral, the observability extras are not.
(`make` is not in Git-Bash anyway, which closes the porting argument.)

Exit: `./scripts` green-listed; TESTING.md section merged; gw-f2w
closed citing the workflow decision.

## Tranche D — environment-dependent packages

- **`test/dolttest`, `examples/bd/dolt`, `examples/gastown`**: spawn
  real dolt servers (dolt.exe is on the box; leaks are contained by
  the job objects now). Canary → classify → fix. Risk note: these are
  the packages most likely to surface doltlite/bd backend divergence,
  which belongs upstream in beads — file separate beads rather than
  patching around backend behavior.
- **`internal/runtime/k8s`**: needs a cluster or fakes; canary first —
  if tests already fake the client, treat as a normal Tranche-C-style
  package; if they need real infra, they gate on infra presence (as
  they must on Linux dev boxes too) and join the green list with that
  self-gating intact.
- **`internal/runtime/t3bridge`**: exercises the T3 Code bridge; may
  require the sibling t3code checkout. Same canary-then-classify
  approach; coordinate with the t3code integration mission before
  changing any bridge behavior.

Exit: each either green-listed or self-gating on absent infra (the
same behavior it has on Linux).

## Tranche E — systemd-parity feature phases

Feature work, not test parity; already designed in
`windows-systemd-parity.md` with per-phase test matrices:

- **gw-x1k (P1)**: Task Scheduler hosting + keepalive `gc supervisor
  ensure` + `GC_NOTIFY_PIPE` sdnotify transport. Dissolves the
  supervisor-test gates from A3.
- **gw-i1l (P2)**: agent containment via `winjob` (suspend→assign→
  resume through psmux).
- **gw-8su (P3)**: workspacesvc orphan discovery on proctable/PEB +
  `pidutil` creation-time identity (also closes the documented PID-
  recycling caveat in `proctable/kill_windows.go`).

Sequenced last: they are additive capabilities whose test matrices are
already written. Most prerequisite primitives (`winjob`,
`pidutil.Cmdline`, TCP attribution) exist; the exception is
creation-time PID identity (`GetProcessTimes` in `pidutil.StartTime`),
which is P3's own first deliverable, not a preexisting input.

## Sequencing

**R → C → A → B → D → E.** R is small and de-conflicts A; C banks 16
packages quickly and may eliminate classes from A's inventory; A is
the long pole; B is an afternoon; D depends on nothing; E is feature
work reviewed against its own spec.

After A lands, the green list covers all but a handful of
infra-gated packages — execute the file's own endgame note: invert it
into a short exclusion list in `windows-gate.yml`.

## Risks

- **cmd/gc gate runtime** — mitigated by A0's measure-first sharding.
- **Upstream drift** — every tranche touches upstream-owned files;
  keep patches minimal per AGENTS.md, and run
  `git range-diff upstream/main...HEAD` before starting A and after
  finishing it. Sweep changes (the 63-site LookPath switch) land as
  mechanical single-purpose commits so they cherry-pick/upstream
  cleanly; **gw-gdh** (reconciling with the upstream
  `feature/reconciler-win-*` branches) executes alongside this step.
- **gw-9k7** (host commit-limit exhaustion) is likely resolved by the
  containment work — recommend closing or re-scoping after a quiet
  week rather than carrying it open indefinitely.
- **testscript unknowns** — bounded by giving the txtar corpus its own
  session and its own strategy (A4) instead of folding it into suite
  sweeps.
- **Behavioral drift disguised as parity** — the doctrine's rule
  stands: when a test disagrees with production about platform
  semantics, decide the *production* semantics first (the
  searchpath/api contract collision is the cautionary example).
