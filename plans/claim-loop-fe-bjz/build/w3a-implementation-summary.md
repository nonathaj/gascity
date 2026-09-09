---
schema: gc.build.implementation-summary.v1
workflow:
  id: gcty-3uo7
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
    - path: beads/gcty-bsi
      hash: bead:gcty-bsi
      ids:
        - REQ-002
        - REQ-006
    - path: plans/claim-loop-fe-bjz/build/w3a-attribution.md
      hash: sha256:c28b91a0f16c11f8602236f68ade48c64ecde1d88763536ca9a61f048aab118a
    - path: cmd/gc/session_reconciler.go
      hash: git:dfb4d5ab5bd20d7a12d38407aea79844629ef2f1
  coverage:
    - id: W3A-1
      status: covered
    - id: W3A-2
      status: covered
    - id: W3A-3
      status: covered
    - id: REQ-002
      status: deferred
      rationale: >-
        This bead is an investigation whose output is a written attribution. It
        names the component and files that serve the stale claimable entry, which
        is the prerequisite W3B needed, but it does not implement the
        store-beats-cache reconciliation REQ-002 specifies. W3B carries REQ-002.
    - id: REQ-006
      status: deferred
      rationale: >-
        This bead identifies the writer that appended to the closed row and why
        the write was accepted. It does not implement the refusal REQ-006
        specifies; W6 carries that. The spurious note is deliberately left in
        place per REQ-007.
---

# W3A implementation summary — attribution of the `fe-bjz` claim loop

## Summary

Bead `gcty-bsi` is an investigation bead: its deliverable is a written attribution,
not a code change. All three attributions it asked for were produced and are recorded
in `plans/claim-loop-fe-bjz/build/w3a-attribution.md`.

1. **Stale claimable entry** — served by the controller's in-process `CachingStore`
   through a *positive-only* wisp probe: `sessionHasOpenAssignedWispWork`
   (`cmd/gc/session_reconciler.go:4584-4595`) returns `true` on a cache hit and never
   consults the authoritative store; only a cache negative reaches the `live=true`
   read. `CachedList` (`internal/beads/caching_store_reads.go:186-210`) refuses
   closed-inclusive queries outright, so the path structurally cannot see that the row
   is closed. `fe-bjz` is `gc.kind: wisp`, so it takes exactly this path.
2. **`bead.updated status=open` re-emitter** — there is **no second emitter, and no
   recurring emission at all**. Exactly one `status=open` exists in the whole log, 184 µs
   before the single `bead.dead_assignee_reopened` and part of the same write.
3. **Closed-row scan note** — written by the `mol-dog-stale-db` formula's own
   `append_report_note()` (line 103, write at line 107, called at line 199) running as
   session `fe-eov5gu` against `$GC_BEAD_ID` = `fe-bjz`.

Two findings correct the plan's premises and change downstream work: the file OQ2 named
(`cmd/gc/hooks.go`) is not the implementation, and OQ3's assumed second emitter does not
exist.

## Intended Behavior

No runtime behavior changes in this bead. The intended effect is informational: W3B and W6
were both blocked on a target this bead names, and both now have one.

- W3B (REQ-002/AC-1) gets the four-site path above, with
  `sessionHasOpenAssignedWispWork` identified as the minimal fix point, plus notice that
  `gc hook --claim` has no closed-row guard of its own and needs one too.
- W6 (REQ-006/AC-4) gets the exact writer, the reason the write was accepted
  (`bd update --append-notes` does not refuse closed rows and the formula swallows append
  failures), and the confirmation that the note is a genuinely fresh scan rather than a
  replay.
- W3B/W5 are explicitly told **not** to hunt a second `status=open` emitter, and REQ-005's
  proposed alarm threshold is flagged for re-checking because this incident produced
  exactly one reopen, not a recurring series.

## Changed Files

| File | Change |
| --- | --- |
| `plans/claim-loop-fe-bjz/build/w3a-attribution.md` | New. The attribution deliverable: evidence base and its limits, timeline, three findings with file:line citations and confidence, direct answers, and guidance for W3B/W6. |
| `plans/claim-loop-fe-bjz/build/w3a-implementation-summary.md` | New. This artifact. |

No source files were modified. The bead's acceptance is a written attribution; changing
runtime code here would exceed its scope and pre-empt W3B/W5A/W6.

## Verification

Work was done in the isolated worktree `worktrees/gcty-bsi` (`pwd -P` verified equal to the
`work_dir` published on the source anchor before any read, edit, or commit); the launcher
checkout was never edited.

**First verification command** — the event census that grounds Finding 2, run against the
city event log:

```
grep -F 'fe-bjz' .gc/events.jsonl | python3 -c '<census: group by subject/type/actor>'
```

Observed: **pass** — 4 events have `subject == fe-bjz` (3 `bead.updated` from actor
`cache-reconcile`, 1 `bead.dead_assignee_reopened` from actor `gc`). Exactly one carries
`status=open`, at `02:46:10.182860-04:00`, 184 µs before the reopen event. The
`session.drain_acked_with_assigned_work` series is 72 events, median gap 130.2 s, all
`bead_status: in_progress`, across only 2 distinct sessions.

Supporting checks, all observed **pass**:

- Substring-artifact test over bd.dog's own 60-minute window: 218 lines *mention*
  `fe-bjz`, only 3 have `subject == fe-bjz` — reproducing and explaining the reported
  "223 events".
- Live differential read at `04:44-04:00`: controller API and `bd` both report `fe-bjz`
  `closed` / `bd__dog-1-pool`, with controller uptime 11h12m spanning the incident —
  showing the divergence was process-local and repaired in place.
- Source confirmation of the positive-only probe and of `CachedList` refusing
  closed-inclusive queries, read at worktree `dfb4d5ab5`.
- Freshness of the spurious note: its `rigs_protected` lists 14 rigs including
  `gem-federation` and `gascity`; the legitimate 2026-08-27 note lists 12 and has neither.

**Final proof command** — the artifact gate itself, exercising bead metadata resolution,
workflow-root lookup, artifact-path resolution and schema validation:

```
GC_BEAD_ID=gcty-p566 GC_WORK_DIR=<pack repo> \
  ${GC_CITY}/.gc/scripts/checks/build-artifact-valid.sh
```

Observed: **pass**, exit 0 —
`build artifact valid: schema=gc.build.implementation-summary.v1 path=<this file>`.

An earlier direct validator invocation
(`python3 <pack>/gascity/assets/scripts/validate_build_artifact.py --schema
gc.build.implementation-summary.v1 --path <this file>`) also passes with
`{"ok": true, "schema": "gc.build.implementation-summary.v1"}`. Its first run **failed**
with `error: markdown coverage matrix is missing` — the artifact carried `trace.coverage`
in front matter but no matching Markdown matrix. A `## Coverage` table was added and both
commands re-run to the passes above.

**The gate does not run unaided in this rig, which independently confirms plan OQ6.**
Measured:

| Invocation | Result |
| --- | --- |
| `.gc/scripts/checks/build-artifact-valid.sh` from the launcher rig root, as the step prompt specifies | exit 127, no such file — the rig root has no `.gc/scripts` at all; the script lives at the **city** root |
| city-root gate, no `GC_WORK_DIR` | exit 1, `validate_build_artifact.py not found beside .../checks or under GC_WORK_DIR` |
| city-root gate, `GC_WORK_DIR` supplied | exit 0, valid |

Note the middle row got *past* bead lookup, root resolution and artifact-path resolution
before failing, which confirms `gc.implementation.summary_path` was recorded correctly on
workflow root `gcty-3uo7`. The only broken link is validator resolution: `GC_WORK_DIR` is
unset in the worker environment and the workflow root's `gc.work_dir` is empty, so neither
candidate path resolves.

## Remaining Risks

- **No live reconciler trace covers the incident.** The loop's last drain-ack was
  `02:55:01-04:00`, ~1h40m before this investigation began; `gc trace status` reports
  `Active trace arms: 0` and no trace data exists for the window. Findings rest on the
  full event log, persisted rows, a live differential read, and the source path. Finding 1's
  code path is high-confidence; that it was the *sole* contributor is medium-confidence.
  The loop has not recurred, so it could not be re-armed and reproduced.
- **The exact eviction that ended the loop is not attributed.** The entry converged without
  a controller restart, which locates the divergence in cache state, but the specific
  reconcile pass that corrected it was not identified. This does not affect the fix target.
- **`gc hook --claim`'s missing closed-row guard is asserted from source, not exercised.**
  No test was written here — writing one belongs to W3B under REQ-008.
- **The artifact gate is not enforcing in this rig (OQ6).** Until the `GC_WORK_DIR` /
  validator-resolution gap is fixed, producer stages in this rig self-validate. That is a
  standing hole in the build's quality gate, not specific to this bead.
- **Scope held deliberately.** The spurious note was not deleted or rewritten (REQ-007),
  and no runtime code was changed, so the loop's *cause* remains live until W3B lands. If
  a `bd.dog` wisp is claimed and stranded again, the same loop can recur.
- **Out of scope but rising in severity:** REQ-011 / `gcty-suj` (one identity spelled two
  ways) was confirmed a third time this session, newly including `bd heartbeat` refusing
  the lease holder its own renewal. Recorded in the attribution's closing section.

## Coverage

| ID | Status | Note |
| --- | --- | --- |
| W3A-1 | covered | Stale claimable entry attributed to the controller's in-process `CachingStore` via the positive-only wisp probe, with file:line citations. |
| W3A-2 | covered | `status=open` re-emission attributed to the single dead-assignee reopen; second emitter explicitly established not to exist. |
| W3A-3 | covered | Closed-row scan note attributed to `mol-dog-stale-db.toml` `append_report_note()` run by session `fe-eov5gu`. |
| REQ-002 | deferred | Attribution only; the store-beats-cache fix lands in W3B. |
| REQ-006 | deferred | Attribution only; the closed-row write refusal lands in W6. |
