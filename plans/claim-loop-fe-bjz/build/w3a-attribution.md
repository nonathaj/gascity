# W3A — Attribution: stale-entry emitter, `status=open` re-emitter, closed-row note writer

Bead: `gcty-bsi` (investigation; output is a written attribution, not a code change).
Author: `gascity/gc.implementation-worker` session `fe-9d20i0`, 2026-09-09.
Incident subject: `fe-bjz` = `mol-dog-stale-db`, a **wisp** (`gc.kind: wisp`) routed to
`bd.dog`, closed `2026-08-27T20:01:53Z` with reason
`Stale DB scan complete (orphans=0, applied=0, escalated=0)`.

## Evidence base and its limits

| Source | What it covers |
| --- | --- |
| `${GC_CITY}/.gc/events.jsonl` | `2026-09-08T09:59:01-04:00` → `2026-09-09T04:38:30-04:00`; spans the entire loop (`00:05:55` → `02:55:01` on 09-09) with no rotation gap. Archives exist only for earlier `seq` ranges. |
| `gc trace status` | Controller running, PID 2547810, **`Active trace arms: 0`**. |
| `ps -o lstart` on PID 2547810 | Started `Tue Sep 8 17:30:58 2026`; uptime 11h12m at time of writing — the controller did **not** restart across the incident. |
| Controller HTTP API vs `bd` | Differential read of `fe-bjz` taken live at `04:44-04:00`. |
| Source | worktree at `dfb4d5ab5`. |

**Limitation, stated plainly.** The bead asks for confirmation "from a live trace, not
assumed". That was not possible: the loop's last `session.drain_acked_with_assigned_work`
fired at `2026-09-09T02:55:01-04:00`, ~1h40m before this investigation began, no trace was
armed while it ran (`Active trace arms: 0`), and `.gc/runtime/session-reconciler-trace/`
holds nothing for the window. The loop has not recurred since. The attributions below rest
on the event log, the persisted bead rows, a live differential read of the still-running
controller, and the source path — not on a captured reconciler trace. Where that leaves a
residual gap it is named in "Confidence" per finding.

## Timeline (all `-04:00` unless the stamp says `Z`)

| Time | Event |
| --- | --- |
| `2026-08-27T20:01:53Z` | `fe-bjz` closed. Legitimate scan note written 1s earlier at `20:01:52Z`. |
| `2026-09-09T00:02:22.039` | `bead.updated` — `status=in_progress`, `assignee=fe-eov5gu`, actor `cache-reconcile`. |
| `2026-09-09T04:04:48Z` (= `00:04:48`) | Spurious `## scan (dry-run)` note appended to the closed row. |
| `2026-09-09T00:05:55.896` | First `session.drain_acked_with_assigned_work`. |
| … 71 more, median gap **130.2s** … | All `bead_status: in_progress`, template `bd.dog`. |
| `2026-09-09T02:46:10.182860` | `bead.updated` — `status=open`, actor `cache-reconcile`. **The only one in the log.** |
| `2026-09-09T02:46:10.183044` | `bead.dead_assignee_reopened` (+184 µs), `dead_assignee=fe-eov5gu`. |
| `2026-09-09T02:49:28.895` | `bead.updated` — `status=in_progress`, `assignee=fe-qq9v8z`. |
| `2026-09-09T02:53:48` | bd.dog escalation mail `fe-wisp-t9qadox` to `fed.mayor`. |
| `2026-09-09T02:55:01.304` | Last drain-ack. Loop ends. No recurrence. |

## Finding 1 — the stale claimable entry is served from the controller's in-process `CachingStore`, through a positive-only wisp probe

**Attributed.** Component: the controller's in-process `CachingStore`, reached by the
session reconciler's *positive-only* wisp assigned-work probe. The concrete path:

| # | File:line | Role |
| --- | --- | --- |
| 1 | `cmd/gc/api_state.go:231-252` `wrapWithCachingStore` | Builds `beads.NewCachingStore(baseStore, onChange)` and stamps every projected event with `Actor: "cache-reconcile"` (line 246). |
| 2 | `cmd/gc/session_reconciler.go:4526-4547` `sessionHasAssignedWorkInStoreByIdentifiersForStatuses` | For statuses `{open, in_progress}` runs the issues-tier probe, then the wisp probe. |
| 3 | `cmd/gc/session_reconciler.go:4584-4595` `sessionHasOpenAssignedWispWork` | **The defect.** Positive-only. |
| 4 | `cmd/gc/work_assignment.go:65-82` `CachedOpenAssignedWisps` | `ListQuery{Assignee, Status, TierMode: TierWisps}` — note: **no `Live` flag** — via `CachedList`. |
| 5 | `internal/beads/caching_store_reads.go:186-210` `CachedList` | Serves purely from the in-memory `c.beads` map. |

Site 3 is the load-bearing one:

```go
if items, ok := wa.CachedOpenAssignedWisps(assignee, status); ok {
    if wa.HasNonSessionWork(items) {
        return true, nil          // cache says yes -> return, never consult backing
    }
}
return sessionHasOpenAssignedWorkForTier(store, assignee, status, beads.TierWisps, true)
```

A cache **hit** short-circuits and returns `true`. Only a cache **negative** falls through
to the authoritative `live=true` read. So a stale in-memory row is self-sustaining: it can
never be contradicted on this path. Site 5 seals it — `CachedList` opens with
`if query.IncludesClosed() { return nil, false }`, so this path is structurally incapable
of observing the closed truth. Contrast the issues tier, which reaches
`OpenAssignedTo(..., live=true)` and does hit `c.backing.List`.

`fe-bjz` takes this path because it is `gc.kind: wisp`.

**Evidence tying the mechanism to the incident.** All 72 `session.drain_acked_with_assigned_work`
events carry `bead_status: "in_progress"` — the cached value — while the persisted row had
read `closed` since 2026-08-27. That status is read at
`cmd/gc/session_reconciler.go:396-431` (`recordDrainAckAssignedWorkEvent`) from
`firstOpenAssignedWorkBeadForReachableStore`, the same store surface. Every `bead.updated`
on `fe-bjz` carries actor `cache-reconcile`, i.e. site 1's projection callback.

**Convergence evidence that the divergence was process-local.** The controller never
restarted (uptime spans the incident and continues), yet a live differential read at
`04:44` now shows agreement:

```
controller API /v0/city/federation/bead/fe-bjz  -> status: closed, assignee: bd__dog-1-pool
bd show fe-bjz (authoritative)                  -> status: closed, assignee: bd__dog-1-pool
```

The entry was therefore corrected **in place**, in memory, with the process still running.
Divergence that appears and disappears without touching disk is cache state, not store
state. This is corroboration, not proof of the exact eviction that ended it.

**Correction to the plan.** OQ2's prime suspect is **confirmed at the component level** —
it is the controller's in-process `CachingStore`. But the file OQ2 names, `cmd/gc/hooks.go`,
is **not** the implementation: it is a 2975-byte file containing only two *comments*
referring to the CachingStore (lines 12 and 55). W3B should target sites 3-5 above, with
site 3 the minimal fix.

**Also note:** `gc hook --claim` itself has **no closed-row check at any point**.
`tryHookClaim` (`cmd/gc/cmd_hook_claim.go:133-177`) trusts the work-query output verbatim,
and `hookClaimExistingOrAssigned` (line 306) matches only on
`status ∈ {in_progress, open}` + identity, yielding `existing_assignment` / `ready_assignment`.
That matches the observed `reason=existing_assignment`. So REQ-001/REQ-002 need a guard on
the hook path too, not only in the cache — the hook would re-serve a closed row from any
work query that offered one.

**Confidence: high** on the code path and on cache-vs-store divergence; **medium** on this
being the *sole* contributor, since no armed trace covers the window.

## Finding 2 — the single `status=open` is the dead-assignee reopen. There is NO second emitter.

**Attributed, and the premise of OQ3 is refuted.**

Across the whole log, exactly **four** events have `subject == fe-bjz`:

| seq | ts | type | actor | status |
| --- | --- | --- | --- | --- |
| 717590 | `00:02:22.039` | `bead.updated` | `cache-reconcile` | `in_progress` (`fe-eov5gu`) |
| 731615 | `02:46:10.182860` | `bead.updated` | `cache-reconcile` | **`open`** |
| 731616 | `02:46:10.183044` | `bead.dead_assignee_reopened` | `gc` | — |
| 731903 | `02:49:28.895` | `bead.updated` | `cache-reconcile` | `in_progress` (`fe-qq9v8z`) |

The only `status=open` precedes the reopen event by **184 microseconds**. They are one
logical write: `releaseOrphanedPoolAssignments` set the row open and
`emitDeadAssigneeReopenedEvents` (`cmd/gc/dead_assignee_event.go:25-46`, called from
`cmd/gc/city_runtime.go:2225`) recorded it; the `bead.updated` is the CachingStore
`onChange` projection of that same write.

**There is no recurring `bead.updated status=open` at all** — not every few seconds, not
ever. Exactly one occurred, at the very end of the loop, and it is fully accounted for.

**Where the "223 events / repeating `bead.updated(open)` cycle" came from.** It is a
substring-match artifact. Measuring bd.dog's own 60-minute window
(`01:53:48`→`02:53:48`):

- lines **mentioning** the string `fe-bjz`: **218** (bd.dog reported 223)
- events whose **subject** is `fe-bjz`: **3** (2 `bead.updated`, 1 reopen)

The 215-event difference is dominated by bd.dog's own escalation mail `fe-wisp-t9qadox`
and bug `gcty-5e1`, whose bodies quote `fe-bjz` repeatedly and which `cache-reconcile`
re-emits on every touch (create, read-flag, sweep, close). The report was substantially
measuring its own paper trail. I reproduced the same error before catching it.

**What the ~2-minute cadence actually is:** the `session.drain_acked_with_assigned_work`
series — 72 events, median gap **130.2s** (min 109.0s, max 612.3s), every one
`bead_status: in_progress`. And it was **not** a spawn-per-tick loop: only **two** distinct
sessions appear, `fe-eov5gu` (×71) and `fe-qq9v8z` (×1). One long-lived session re-acked
for 2h45m.

**Ruled out:** `session.demand_claim_divergence` — 0 occurrences for `fe-bjz`, despite a
city-wide baseline of ~80-120/hour on other beads. Not implicated.

**Consequence for the build.** OQ3 asked which *second* emitter to fix. The answer is that
none exists, so W3B/W5 must not spend effort hunting one, and REQ-005's alarm threshold
("third reopen of one bead within a patrol window") is triggered by a population of exactly
one reopen here — worth re-checking against real data before it is tuned.

**Confidence: high.** This is a census over the full window, not a sample.

## Finding 3 — the closed-row note was written by the `mol-dog-stale-db` formula itself

**Attributed.** Writer: the formula's own report-append helper, run by the `bd.dog` session
holding the stale claim.

- **File:** `examples/bd/dolt/formulas/mol-dog-stale-db.toml` in the pack cache
  (`/home/jkenkel/.gc/cache/repos/afb89b17…/`), the exact path recorded in `fe-bjz`'s own
  `gc.formula_source`.
- **Function:** `append_report_note()`, line 103. **Write:** line 107 —
  `bd update "$WORK_BEAD" --append-notes "$(printf '## %s %s\n\n```json\n%s\n```' …"$(date -u +"%Y-%m-%dT%H:%M:%SZ")"…)"`
- **Call site:** line 199, `append_report_note "scan (dry-run)" "$SCAN_FILE"` — which
  renders exactly the observed heading `## scan (dry-run) 2026-09-09T04:04:48Z`.
- **Target:** line 88, `WORK_BEAD="${GC_BEAD_ID:?GC_BEAD_ID required (set by gc hook); aborting}"`
  — i.e. whatever `gc hook --claim` served. **Finding 3 is a direct consequence of Finding 1.**
- **Session:** `fe-eov5gu` (template `bd.dog`, pool `bd__dog-1-pool`). The note's `date -u`
  stamp `04:04:48Z` = `00:04:48-04:00`, sitting between the claim (`00:02:22`) and the
  first drain-ack (`00:05:55`).
- **Store corroboration:** `fe-bjz.updated_at = 2026-09-09T04:04:49Z` on a row closed
  `2026-08-27T20:01:53Z`.

**Proof it is a fresh scan, not a replayed note.** The 2026-09-09 payload's
`rigs_protected` lists 14 rigs including `gem-federation`/`gf` and `gascity`/`gcty`; the
legitimate 2026-08-27 note lists 12 and has neither. Those rigs were registered in between.
The command genuinely ran on 09-09 and wrote its result to a row closed 13 days earlier.

**Why the write succeeded.** `bd update --append-notes` does not refuse closed rows, and
the formula has no closed-row guard — REQ-006 is simply unimplemented, at both layers.
Worse for detection: `append_report_note` swallows its own failure
(`echo "failed to append ${title} report; continuing to drain-ack" >&2`), so even a refusal
would not have surfaced as an error. REQ-006 should refuse the write *and* the formula
should stop treating an append failure as ignorable.

**History preserved.** The spurious note is left in place, per REQ-007. This document is
the record of what happened; nothing was rewritten or deleted.

**Confidence: high.** The heading format, the UTC stamp, the target-bead resolution, the
`updated_at`, and the rig-list delta all agree.

## Answers to the three questions asked

1. **Which component serves the stale claimable entry for a row the store reports CLOSED?**
   The controller's in-process `CachingStore`, via the positive-only wisp probe
   `sessionHasOpenAssignedWispWork` (`cmd/gc/session_reconciler.go:4584-4595`) →
   `CachedOpenAssignedWisps` (`cmd/gc/work_assignment.go:65-82`) → `CachedList`
   (`internal/beads/caching_store_reads.go:186-210`), constructed at
   `cmd/gc/api_state.go:231-252`. `gc hook --claim` additionally has no closed-row guard of
   its own.
2. **What re-emits `bead.updated status=open` every few seconds — is there a second emitter?**
   **Nothing does, and there is no second emitter.** Exactly one `status=open` occurred, at
   `02:46:10.182860`, 184 µs before and belonging to the same write as the single
   `bead.dead_assignee_reopened`. The "every few seconds" cycle was a substring-match
   artifact; the real cadence is the drain-ack series, which reports `in_progress`.
3. **Which writer produced the spurious `2026-09-09T04:04:48Z` scan note?**
   `mol-dog-stale-db.toml` `append_report_note()` (line 103, write at line 107, called at
   line 199), run by session `fe-eov5gu`, targeting `$GC_BEAD_ID` = `fe-bjz` as served by
   the stale claim.

Nothing in the three questions was left unattributed.

## Incidental confirmation — REQ-011 / `gcty-suj`

Not this bead's scope; recorded because it was observed first-hand and the requirements doc
tracks prior sightings. One identity spelled two ways broke a **third** operation this
session. The dispatcher stamps `assignee` with the *session id* (`fe-9d20i0`) while `bd`
and the claim-verification predicate resolve "me" from `BEADS_ACTOR` (the *session name*,
`gascity--gc__implementation-worker-4-pool`). Consequences observed:

1. the startup claim block rejected its own valid bead in an unbounded retry loop
   (`CLAIM_REJECTED assignee mismatch`, previously reported);
2. `bd close` blocked (previously reported);
3. **`bd heartbeat gcty-p566` → `Error: heartbeat gcty-p566: issue already claimed by fe-9d20i0`**
   — refusing the holder its own lease. `BEADS_ACTOR=fe-9d20i0 bd heartbeat …` succeeds.

Item 3 is new: the mismatch does not merely block claiming and closing, it makes a held
lease **unrenewable** by its holder, so long work self-destructs at lease expiry. That
raises `gcty-suj`'s severity beyond how REQ-011 currently describes it.

## What W3B and W6 should take from this

- **W3B (REQ-002/AC-1):** fix `sessionHasOpenAssignedWispWork` so a positive cache answer is
  validated against the store before it is trusted — that is the minimal change. Do **not**
  look for a second `status=open` emitter; there is none. Add the hook-path guard too, since
  `gc hook --claim` will re-serve any closed row a work query offers.
- **W6 (REQ-006/AC-4):** the writer is named above. The refusal belongs in `bd update
  --append-notes` for closed rows; additionally, `append_report_note` should not swallow the
  failure.
- **Re-check REQ-005's threshold.** It assumes recurring reopens. This incident produced
  exactly one.
