---
schema: gc.build.implementation-summary.v1
workflow:
  id: gcty-wap1
  formula: do-work
methodology:
  pack: gascity
  name: build-basic
producer:
  formula: do-work
  stage: implement
  attempt: 1
status: approved
trace:
  upstream:
    - path: beads/gcty-htd5
      hash: bead:gcty-htd5
      ids:
        - REQ-006
        - AC-4
    - path: plans/claim-loop-fe-bjz/build/w3a-attribution.md
      hash: git:1701a8cd3d477cc91d489e6189d0a99185215345
    - path: examples/bd/dolt/formulas/mol-dog-stale-db.toml
      hash: sha256:5418964625cec923242198e056b32c224dfbbbfd72eba3f16b534f4b140f54ff
    - path: examples/bd/dolt/stale_db_formula_test.go
      hash: sha256:20f6f1794bfb369af74c29faa1dc7e58efcc122f8ed9996e54f90d9510c966e5
  coverage:
    - id: REQ-006
      status: covered
    - id: AC-4
      status: covered
    - id: W6-1
      status: covered
    - id: W6-2
      status: covered
    - id: W6-3
      status: covered
---

# W6 implementation summary — refuse note/report writes to closed beads

## Summary

Bead `gcty-htd5` is the **gate half** of plan work item W6, split from the locate
half (W3A, `gcty-bsi`) per plan-review finding F3. W3A named the writer; this bead
gates it.

The writer W3A attributed is `append_report_note()` in
`examples/bd/dolt/formulas/mol-dog-stale-db.toml` — the formula that produced the
spurious `## scan (dry-run) 2026-09-09T04:04:48Z` note on `fe-bjz`, a row closed
`2026-08-27T20:01:53Z`. It wrote to whatever `gc hook --claim` served as
`$GC_BEAD_ID`, and `bd update --append-notes` does not itself refuse closed rows,
so a stale claim landed a fresh scan report on a row closed 13 days earlier.

The formula now establishes the work bead's status before every report append and
refuses the write when the row is closed — or when its status cannot be
established at all. The refusal is surfaced as an error, not swallowed.

`bd` is a separate tool and is not modified here; the refusal is implemented at the
only layer this bead owns, which is also the layer that actually issued the write.

## Intended Behavior

`append_report_note()` gains a precondition, `refuse_write_to_closed_bead`, that
runs before the `bd update --append-notes` call at each of the six report sites:

- **Row is open** — unchanged. The report note is appended exactly as before.
- **Row is closed** — the append is refused. The formula prints
  `refusing to append <title> report on closed bead <id>; the claim is stale and
  the closed row is left unmodified` on stderr and exits non-zero through the
  file's existing `fail_open_after_drain` idiom, so the drain-ack contract is
  still honored. No `bd update` and no `bd close` reaches the closed row.
- **Status cannot be established** — treated the same as closed, and reported as
  `refusing to append <title> report: cannot read the status of <id>`. This covers
  both `bd show` exiting non-zero and `bd show` exiting zero with a payload the
  status cannot be read from. Refusing an unverifiable row is deliberate: the
  purpose of the gate is to stop writing to rows whose state is not established.

Status is read with `bd show "$WORK_BEAD" --json`, unwrapping the one-element list
shape that `bd show --json` can return.

The scope stays where the bead put it. This gates REQ-006's note/report write path
only. No general immutability or audit-trail model for closed beads was
introduced, and no separate guard was added to `bd close` — on this path the
refusal aborts before the close is reached.

## Changed Files

| File | Change |
| --- | --- |
| `examples/bd/dolt/formulas/mol-dog-stale-db.toml` | Added `work_bead_status()` and `refuse_write_to_closed_bead()`; `append_report_note()` now calls the gate before appending. 25 lines added, no lines removed. |
| `examples/bd/dolt/stale_db_formula_test.go` | Added `TestStaleDBFormulaRefusesNoteAppendToClosedWorkBead` and table-driven `TestStaleDBFormulaRefusesNoteAppendWhenStatusUnestablished`; taught the 7 existing fake `bd` scripts the `show` subcommand the formula now calls. |

## Verification

Written test-first, per the repo's TDD rule.

**First verification command** — the new closed-row test run against the ungated
formula:

```
go test ./examples/bd/dolt/ -run 'TestStaleDBFormulaRefusesNoteAppendToClosedWorkBead' -count=1
```

**Observed: FAIL**, as intended, and it reproduced the incident directly. The
command log showed the write landing on the closed row:

```
bd update bead-1 --append-notes ## scan (dry-run) 2026-09-09T10:27:24Z
bd close bead-1 --reason Stale DB scan complete (orphans=0, applied=0, escalated=0)
```

**Final proof command** — the whole package after the gate was added:

```
go test ./examples/bd/dolt/ -count=1
```

**Observed: PASS** — `ok github.com/gastownhall/gascity/examples/bd/dolt 202.787s`.

Also observed:

- `go vet ./...` — **PASS**, clean.
- `gofmt -l examples/bd/dolt/` — **PASS**, no files listed.
- The three gate tests individually — **PASS**
  (`TestStaleDBFormulaRefusesNoteAppendToClosedWorkBead`,
  `.../show_exits_non-zero`, `.../show_exits_zero_with_unreadable_payload`).
- The pre-existing suite still passes unchanged, including
  `TestStaleDBFormulaDryRunFailureAppendsScanJSON` (open rows still get their
  note) and `TestStaleDBFormulaSuccessPathFailuresDrainAck` (an append failure on
  an open row is still non-fatal).

The closed-row test asserts all three acceptance statements: the run exits
non-zero with the refusal on stderr, no `--append-notes` is issued, and a separate
mutation log proves the closed row received no `bd update` and no `bd close`.

| ID | Status |
| --- | --- |
| REQ-006 | covered |
| AC-4 | covered |
| W6-1 | covered |
| W6-2 | covered |
| W6-3 | covered |

W6-1 = a note/report append to a closed bead is refused and raises a visible
error. W6-2 = a test asserts the write is refused and the closed row is
unmutated. W6-3 = no general immutability model was introduced.

## Remaining Risks

- **The gate is formula-local, not store-local.** `bd update --append-notes`
  still accepts closed rows for every other caller. W3A recommended the refusal
  belong in `bd` itself; `bd` is a separate tool outside this repo and outside
  this bead's stated scope, so any other writer can still append to a closed row.
  This is a containment of the attributed writer, not a systemic fix.
- **W3A's second recommendation is deliberately not implemented here.** W3A also
  advised that `append_report_note` stop swallowing a *generic* append failure
  (the `failed to append ... ; continuing to drain-ack` warn path). That behavior
  is pinned by the existing `TestStaleDBFormulaSuccessPathFailuresDrainAck/scan_note_failure`
  contract and is outside this bead's acceptance, which covers the closed-row
  refusal only. It is left unchanged and unclaimed; it needs its own bead.
- **This gate does not stop the loop, only its damage.** The stale claim that
  served a closed row is REQ-002's problem and is carried by W3B (`gcty-jmu6`)
  and the claim-admission work in W2 (`gcty-l52m`). With this change the stale
  claim still occurs; it can no longer silently mutate the closed row, and it now
  fails loudly instead, which should make a recurrence visible rather than silent.
- **Fail-closed on an unestablished status is a judgment call.** If `bd show`
  became unavailable, this order would refuse its report and exit non-zero every
  run rather than appending blind. That is the safer direction for this write
  path, and it is loud rather than silent, but it is a behavior change for an
  environment where `bd show` is broken while `bd update` still works. Note that
  the related fail-open/fail-closed decision for *claim admission* is explicitly
  W2's to settle, not this bead's.
- **Two added subprocesses per report append** (`bd show` piped to `jq`). Per
  `engdocs/contributors/windows-portability.md` the unit of cost is `fork()`, and
  this is an order, so the cost recurs. It is accepted here: the order runs on a
  4-hour cron with at most three appends per run, so this is not the hot-loop
  shape rule S4 targets, and the row's status cannot be established without
  asking the store. Caching one status read per run was rejected because the
  check must reflect the row at write time.
