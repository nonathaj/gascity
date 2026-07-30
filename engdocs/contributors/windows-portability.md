# Windows Portability Doctrine

The catalogue of Windows/Linux divergence classes found while driving
the test-parity effort (branch `fix/windows-parity-5`, ~200 fixes,
2026-07), each with its one canonical fix. **Read this before touching
a failing Windows test or writing code that spawns processes, builds
paths into other syntaxes, or checks permissions.** Most "new" Windows
failures are instances of a class below; fix them the catalogued way so
the codebase stays coherent.

## Settled: two-tier nativeness (decided 2026-07-23)

Pack portability is a **requirement**: packs authored on Linux must run
on a Windows city as-is. That decision fixes the architecture of the
port into two tiers — do not relitigate it per-fix:

- **Tier 1 — SDK mechanisms are Windows-native.** Everywhere the SDK
  itself touches the OS, use real Windows mechanisms, never emulation:
  `pidutil` (TCP table, PEB) not `/proc`/`lsof`/`pgrep`, `winjob` Job
  Objects not process groups, `winsec` ACLs not chmod, native
  exclusive-open probes not lsof, Task Scheduler (gw-x1k) not
  systemd/launchd.
- **Tier 2 — user/pack-supplied executable content is POSIX sh.**
  Pack scripts, order `Exec` strings, condition checks, and setup
  scripts are written once, in sh, and run on Windows through Git for
  Windows' `sh` (`internal/execshim`). sh here is an *interpreter
  dependency*, not OS emulation — exactly like a pack tool written in
  Python. Git for Windows is a first-class prerequisite of GasCity on
  Windows (git already is one: rigs are git repos, pack fetch is git
  clone; the standard Windows git distribution ships sh + coreutils).
  POSIX coreutil paths inside that contract (`/bin/echo`) resolve to
  the sh distribution's coreutils (`execshim.LookPath`).

A per-platform variant escape hatch (e.g. a pack shipping `setup.ps1`
beside `setup.sh`, or `exec_windows` on an order) is a legitimate
*future, config-level* feature; forking the builtin packs or the exec
contract per-OS is not.

## Production classes

| # | Class | Canonical fix |
|---|---|---|
| P1 | Exec of scripts / bare coreutils names (`sh`, `true`, extensionless shebang files) | `internal/execshim`: `Command`/`CommandContext` (shebang-routes through sh), `LookPath` (coreutils fallback), `ResolveExecutable` (path-qualified names need existence, not PATHEXT) |
| P2 | Overriding `cmd.Env` after an execshim constructor | Wrap with `execshim.EnvWithShellDir(...)` — a raw override silently discards the sh-dir PATH injection and sh-routed children lose `sed`/`cat`/`mkdir` |
| P3 | `file://` URLs carrying local paths | `file:///C:/x` parses to `u.Path == "/C:/x"`; strip the leading slash before `filepath.FromSlash`. Build URLs as `"file:///" + TrimPrefix(ToSlash(p), "/")` — `file://C:/x` parses `C:` as a host. Centralized as `pathutil.LocalPathFromFileURL` / `pathutil.FileURLForLocalPath`. |
| P4 | Matching slash-form patterns against `filepath.Rel`/native output | `filepath.ToSlash` the native side before comparing. Identifiers that *are* slash-form (bd scope refs, config-authored paths) stay slash-form in output/messages on every platform |
| P5 | Unix permission-bit semantics (`0600` secrets, permissive-mode checks, mode preservation) | Mode bits are synthetic on Windows (`os.Stat` → 0666/0444; NTFS ACLs govern access). Checks report **not-applicable** on Windows (see `ServiceSecretsPermsCheck`, `pgauth`); never flag or "fix" modes there |
| P6 | "Am I / is this a Go test binary?" guards | `execshim.IsGoTestExecutable` only. A bare `.test` suffix check misses `.test.exe` — that exact miss fork-bombed the host (incident gw-8g5) |
| P7 | `/proc` dependencies (cmdline, port owners, subreaper) | `pidutil.Cmdline` (PEB walk) and doctor's `GetExtendedTcpTable` port-holder arm are the Windows equivalents; `pidutil.AliveWithCmdline` verifies for real on linux+windows and fails closed. Darwin falls back via `ErrCmdlineUnsupported` |
| P8 | Embedding native paths into other syntaxes (sh command text, JSON, TOML templates) | The consuming syntax eats backslashes. sh: `filepath.ToSlash` + quote (`%q`). JSON: `json.Marshal` the value, never string-concatenate. TOML templates: values are escaped centrally in `materialize.expandMCPTemplate` |
| P9 | Windows never tears down process trees | Containment is **automatic**: `internal/testenv`'s init puts every test binary in a kill-on-close Job Object (`internal/winjob`); `testutil.StartExitWatchdog` bounds binaries (`max(25m, -test.timeout+2m)`). Production self-spawns are guarded (P6) with a `TestMain` argv backstop in `cmd/gc` and `internal/session` |
| P10 | Symlink replacement | `MoveFileEx` cannot replace a directory-symlink reparse point; `materialize.atomicSymlink` falls back to remove-then-rename on Windows (documented non-atomic window) |
| P11 | A native-Windows build of a tool emits CRLF (opens stdout in text mode). `jq` is the ONLY such tool the packs depend on — every other coreutil (`sed`, `grep`, `awk`, `head`, `cat`, `mktemp`) comes from the sh distribution and already emits LF | Source the platform prelude (`internal/bootstrap/packs/core/assets/scripts/_platform.sh`), which shadows `jq` with a function adding `-b` on Windows only. **Not** per-call strips at the consumer: MSYS pipes translate CRLF→LF, so `jq \| while read` looks correct while a process substitution, here-string or file redirect does not — the bug is shape-dependent, silent, and invisible on Linux, so no author-remembered idiom survives. `-b` must never reach a Unix jq: it is recognized outside jq's `#ifdef WIN32` but its `continue` is inside, so on Unix the argument falls through to the unknown-option abort. Defining the wrapper only on Windows keeps it off Unix regardless of jq version (`-b` is jq ≥ 1.7). Detect with `OS=Windows_NT` **plus a `uname` fallback** — `OS` is inherited, not intrinsic, so `env -i` wrappers and Go tests that assign `cmd.Env` from a fixed list drop it and leave the shim silently inert (gw-14t) |

## Test-fixture classes

| # | Class | Canonical fix |
|---|---|---|
| T1 | `t.Setenv("HOME", …)` | Always pair with `t.Setenv("USERPROFILE", …)` — `os.UserHomeDir` reads USERPROFILE on Windows |
| T2 | Absolute-path fixtures like `/tmp/x`, `/srv/y` | Not `filepath.IsAbs` on Windows. Use `t.TempDir()`, or drive-prefix + `FromSlash` (`C:` + p) for pure lexical tests, or `t.Chdir` + relative paths |
| T3 | Fake executables on PATH | `LookPath` resolves through PATHEXT: write `name + ".bat"` (`@exit /b 0`) on Windows. execshim-routed spawns can instead use `.sh` fixtures |
| T4 | chmod-based error injection (`Chmod(0)` / read-only dirs) | `Chmod` cannot deny the owner on NTFS. For open/read failures, hold an exclusive no-share handle (`makeFileUnopenable` pattern — sessionlog, eventexport). Stat failures and unwritable-dir cases have no Windows equivalent: skip with a comment naming the mechanism |
| T5 | Long-lived fake subprocesses | `testutil.SelfExpiringHoldScript` (bounded sleep loop). Never `read _hold` (blocks forever when orphaned) or `read -t` (bashism dash lacks) |
| T6 | Timer/TTL tests assuming the clock advances between statements | Windows `time.Now()` ticks at ~0.5 ms; step past a tick (`time.Sleep(2ms)`) between requests that must land in different time buckets |
| T7 | git-based fixtures | Exec bits: `git update-index --chmod=+x` (NTFS `add` records 100644). Content: LF-pin goldens via `.gitattributes`; never rely on host `core.autocrlf` |
| T8 | Paths inside sh fixture scripts | `command -v cygpath >/dev/null 2>&1 && p=$(cygpath -u "$p")` — no-op off Windows. Git-Bash quirks: `curl` lives in `/mingw64/bin`, there is no `wget`, no `python3` |
| T9 | Per-platform goldens (hashes over native paths) | Verify against the tool's actual source before pinning (the Kimi MD5 goldens are verified against kimi-cli's `PureWindowsPath` + `md5(str(path))`) — determinism-only assertions can pass while wrong |
| T10 | Repo-path comparisons in lints/generators | `filepath.Rel` returns backslashes: `ToSlash` before comparing to slash literals (this bug hid in the testenv lint *and* its generator) |
| T11 | 8.3 short names on CI runners | GitHub's TEMP is short-form (`C:\Users\RUNNER~1\…`); production canonicalization expands to the long form, so expectations built from raw `t.TempDir()` mismatch — **only on the runner** (short local usernames need no 8.3 alias). Use `testutil.CanonicalTempDir` (or inline `EvalSymlinks` where testutil would cycle) whenever a test compares canonicalized production output against fixture paths |
| T12 | GitHub-runner environment deltas | No `dolt.exe`, ICMP blocked (`ping`-based slow-command fakes fail — use `powershell -NoProfile -Command Start-Sleep`), `timeout.exe` rejects redirected stdin, no `/tmp` (a bare `MkdirTemp("/tmp", …)` panics; use the default root on Windows) |

## Performance: the cost model is per-operation, not per-byte

Windows is not uniformly slower; it charges far more for *each* filesystem
entry and each process it creates, and almost the same for bytes. Getting this
backwards leads to optimizations that do nothing.

Measured on the dev box (32 core, 128 GiB, NTFS, SentinelOne EDR present),
against typical Linux figures:

| Operation | Windows | Linux | Ratio |
|---|---|---|---|
| `stat` an existing file | 71us | ~1-2us | ~50x |
| open+close an existing file | 114us | ~5us | ~25x |
| **create** a file (open+write+close) | **662us-1.2ms** | ~25us | ~50x |
| `MkdirAll` 3-deep + `RemoveAll` | 5.26ms | ~60us | ~88x |
| spawn `cmd /c exit` | ~100ms | ~1ms | ~100x |
| spawn a script through sh | ~380ms | ~1ms | ~380x |
| 8 MB written through **one** open | 130ms (61 MB/s) | comparable | ~1x |

Reads are cheap-ish, writes through an existing handle are fine, and
throughput is healthy. Creation is what costs.

**No copy strategy avoids this.** Measured per file, same box:
`create+write` 1.175ms, `os.Link` (hardlink) **1.72ms — slower**, `os.Symlink`
1.105ms. NTFS charges for the directory entry whether it points at new data,
shared data, or a reparse point, so `os.CopyFS` (Go 1.23), hardlink farms, and
kernel-side `CopyFileEx` all land on the same floor. The only lever is
**creating fewer entries**.

Consequences that have actually bitten:

- **Atomic writes cost 4x a plain write.** `fsys.WriteFileAtomic` is temp
  create + chmod + rename + orphan-sweep readdir. That is invisible on Linux
  and dominant here. When a file is brand new *and* the caller already holds an
  exclusive lock *and* the result is validated afterwards, write it directly
  (`builtinpacks.writeMaterializedPackFile`). Do not weaken the atomic path for
  files that already exist: a reader must never see a half-written replacement.
- **Re-materializing a content-addressed cache is never free.** One synthetic
  pack repo is 492 files + 220 dirs (2.4 MB) and takes ~1.4s to write here
  versus ~18ms on Linux. A test that rotates `GC_HOME` per subtest rebuilds it
  every time, then pays `RemoveAll` to delete it again.
- **Suite-level effect.** The same six `cmd/gc` unit shards take 7.5 min of
  wall clock on Linux CI and 31 min here (4.1x; 4.7x by total test time), and
  the ratio is uniform across shards -- the signature of a per-operation cost
  applied everywhere rather than a few slow tests.
- **Parallelism does not work around it.** Measured twice: 14 shards took
  58 min against 41 min at 6, and the process suite behaved the same at 3 vs 6
  concurrent shards. Process creation is the contended resource, so more
  concurrency multiplies it.

When a Windows path is slow, profile before optimizing: `go test -cpuprofile`
then `go tool pprof -peek` on the suspect function. Every hypothesis formed
from the shape of the code alone (EDR reputation scanning, a dead
skip-if-unchanged check, an orphan sweeper) was refuted by the profile, which
pointed instead at redundant work the code should not have been doing on
either platform.

The sh-specific breakdown of that spawn cost -- which constructs fork and
what each costs -- is in the Tier 2 section below; it is the same charge
seen from the pack-script side.

## Tier 2 performance: fork cost (measured 2026-07-29)

Choosing POSIX sh for Tier 2 buys pack portability at a price that is
invisible on Linux and severe on Windows. **The unit of cost is
`fork()`, not "shell".** Win32 has no copy-on-write fork, so the sh
distribution emulates it by copying the address space and replaying DLL
base addresses. This is a structural property of the platform, widely
reported outside this project, with no configuration or environment fix
— not antivirus, not a shell flag, not `nsswitch`. Shell *builtins* are
free; every subshell is not.

Measured on a Windows dev box (Defender disabled, so this is not AV
overhead):

| sh construct | Windows cost | Notes |
|---|---|---|
| parameter expansion (`${p##*/}`), `case`, arithmetic | **free** | no process created |
| subshell function body — `f() ( … )` | **~70 ms per call** | paid *before* the body runs |
| command substitution — `$(cmd)` | **~154 ms** | fork even when `cmd` is a builtin (`$(echo x)`) |
| fork + exec of an external coreutil | **~380 ms** | `sed`, `head`, `basename`, … |

The Linux equivalents are sub-millisecond (~0.5 ms fork, 1–2 ms
fork+exec). Those Linux figures are *not* measured here — they are the
standard costs, consistent with the package-level ratios below.

Consequences observed, not projected:

- `examples/bd/dolt` runs its 189 tests in **79 s on Linux CI** and
  exceeds **45 minutes** on Windows without finishing.
- One `orphan-sweep` order invocation costs **~13–15 s** on Windows.
- A single `read_runtime_state_flag` call in the **core** pack — a
  subshell body wrapping `$(sed … | head -1)`, invoked via `$( )` —
  measured **over 1 s**.

This is not confined to an opt-in pack. The core builtin pack that every
city gets carries ~384 command substitutions across 16 scripts, and its
largest scripts (`reaper.sh`, `jsonl-export.sh`, `orphan-sweep.sh`,
`renudge-stale-human-gates.sh`) are **orders** — they fire on a schedule
for the life of the city, so the cost recurs rather than being paid once.

### Rules for writing Tier 2 sh

Ordered by payoff. The first three change nothing about a script's
interface, so they are safe to apply mechanically.

| # | Rule | Measured effect |
|---|---|---|
| S1 | Write function bodies with braces — `f() { … }`, not `f() ( … )` — unless the function genuinely needs subshell isolation (it assigns variables or `cd`s that must not leak). | 2.7× on a helper |
| S2 | Parse with shell builtins instead of spawning: `while IFS= read -r line` + `case` + parameter expansion in place of `$(sed …)`, `$(grep …)`, `$(cut …)`, `$(head -1)`. | 10.7× cumulative |
| S3 | Prefer parameter expansion to a process: `${p##*/}` over `$(basename "$p")`, `${p%/*}` over `$(dirname "$p")`, `read -r x < f` over `x=$(cat f)`. Collapse multi-stage pipelines into one `awk`. | included above |
| S4 | For helpers called in loops, return through a variable instead of `printf`, so the *caller* needs no `$( )` either. Invasive — it changes every call site — so reserve it for hot paths. | ~50× cumulative |

Two rules that are about correctness, not speed, and that the fork-cost
work keeps colliding with:

- A script must locate its siblings from a **slash-separated** path.
  `case "$0" in */*)` silently falls through to `$(pwd)` when handed a
  native Windows path, which resolves the script's directory to the
  *caller's* working directory. The exec boundary normalizes paths so
  pack scripts stay pure POSIX (class P1); do not add backslash handling
  to pack content.
- Do not reach for a tool the sh distribution lacks. Git for Windows
  ships no `flock`, no `lsof`, no `python3`, and no `wget` (class T8).
  Fail *soft* with a portable fallback where the guarantee allows one —
  `mkdir` is atomic on every POSIX filesystem and is the portable
  mutual-exclusion primitive when `flock` is absent — and skip loudly
  where it does not.

## Operational rules

- The green list is `.github/windows-test-packages.txt` (header comment
  explains its lifecycle). A package joins only when fully green **on
  the windows-latest runner** (the dev box has tools — dolt, real
  CLIs — the runner lacks; tool-dependent tests must self-skip when
  the tool is absent), and green-listing a package over ~30 tests
  requires two consecutive clean full runs. A deliberate skip must
  name the mechanism it gates on and, where one exists, the bead that
  will dissolve it.
- New `GC_*` env reads must be added to
  `internal/testenv/testdata/gc_env_read_baseline.golden` (test-gated).
- `internal/testenv` stays dependency-minimal: it is blank-imported by
  every test dir, so anything it imports (`internal/winjob`) cannot
  import it back — those packages are exempted in the lint and the
  generator, in sync.
