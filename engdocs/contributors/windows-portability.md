# Windows Portability Doctrine

The catalogue of Windows/Linux divergence classes found while driving
the test-parity effort (branch `fix/windows-parity-5`, ~200 fixes,
2026-07), each with its one canonical fix. **Read this before touching
a failing Windows test or writing code that spawns processes, builds
paths into other syntaxes, or checks permissions.** Most "new" Windows
failures are instances of a class below; fix them the catalogued way so
the codebase stays coherent.

## Production classes

| # | Class | Canonical fix |
|---|---|---|
| P1 | Exec of scripts / bare coreutils names (`sh`, `true`, extensionless shebang files) | `internal/execshim`: `Command`/`CommandContext` (shebang-routes through sh), `LookPath` (coreutils fallback), `ResolveExecutable` (path-qualified names need existence, not PATHEXT) |
| P2 | Overriding `cmd.Env` after an execshim constructor | Wrap with `execshim.EnvWithShellDir(...)` — a raw override silently discards the sh-dir PATH injection and sh-routed children lose `sed`/`cat`/`mkdir` |
| P3 | `file://` URLs carrying local paths | `file:///C:/x` parses to `u.Path == "/C:/x"`; strip the leading slash before `filepath.FromSlash`. Build URLs as `"file:///" + TrimPrefix(ToSlash(p), "/")` — `file://C:/x` parses `C:` as a host. (Centralization into `pathutil` is planned; see the endgame plan.) |
| P4 | Matching slash-form patterns against `filepath.Rel`/native output | `filepath.ToSlash` the native side before comparing. Identifiers that *are* slash-form (bd scope refs, config-authored paths) stay slash-form in output/messages on every platform |
| P5 | Unix permission-bit semantics (`0600` secrets, permissive-mode checks, mode preservation) | Mode bits are synthetic on Windows (`os.Stat` → 0666/0444; NTFS ACLs govern access). Checks report **not-applicable** on Windows (see `ServiceSecretsPermsCheck`, `pgauth`); never flag or "fix" modes there |
| P6 | "Am I / is this a Go test binary?" guards | `execshim.IsGoTestExecutable` only. A bare `.test` suffix check misses `.test.exe` — that exact miss fork-bombed the host (incident gw-8g5) |
| P7 | `/proc` dependencies (cmdline, port owners, subreaper) | `pidutil.Cmdline` (PEB walk) and doctor's `GetExtendedTcpTable` port-holder arm are the Windows equivalents; `pidutil.AliveWithCmdline` verifies for real on linux+windows and fails closed. Darwin falls back via `ErrCmdlineUnsupported` |
| P8 | Embedding native paths into other syntaxes (sh command text, JSON, TOML templates) | The consuming syntax eats backslashes. sh: `filepath.ToSlash` + quote (`%q`). JSON: `json.Marshal` the value, never string-concatenate. TOML templates: values are escaped centrally in `materialize.expandMCPTemplate` |
| P9 | Windows never tears down process trees | Containment is **automatic**: `internal/testenv`'s init puts every test binary in a kill-on-close Job Object (`internal/winjob`); `testutil.StartExitWatchdog` bounds binaries (`max(25m, -test.timeout+2m)`). Production self-spawns are guarded (P6) with a `TestMain` argv backstop in `cmd/gc` and `internal/session` |
| P10 | Symlink replacement | `MoveFileEx` cannot replace a directory-symlink reparse point; `materialize.atomicSymlink` falls back to remove-then-rename on Windows (documented non-atomic window) |

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
