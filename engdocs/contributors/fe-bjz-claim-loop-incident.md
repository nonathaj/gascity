---
title: The fe-bjz Claim Loop
description: What happened to the wisp fe-bjz, why a scan note dated 2026-09-09 sits on a bead closed 2026-08-27, and what the loop cost. Nothing in the ledger was erased.
---

## What This Record Is

On the night of 2026-09-08/09 a claim loop repeatedly served a **closed** bead,
`fe-bjz`, to dog sessions that could not act on it. One consequence is visible
in the ledger today: `fe-bjz` carries a scan note stamped `2026-09-09T04:04:48Z`
even though the bead closed on `2026-08-27T20:01:53Z`.

This page explains that note. **The note is deliberately left in place.** It is
not a mistake to be cleaned up; it is evidence, and it is the artifact that made
the loop legible. Rewriting ledger history to make the record look tidy is a
named constraint violation for this work.

> **Do not delete or rewrite the `2026-09-09T04:04:48Z` note on `fe-bjz`.**
> If you are here because the note looks wrong: it *is* wrong, that is the
> point, and this page is the explanation it points to.

## Where `fe-bjz` Actually Lives

`bd show fe-bjz` fails from inside a gascity agent session:

```console
$ bd show fe-bjz
Error fetching fe-bjz: no issue found matching "fe-bjz"
```

That failure is a store-scoping artifact, **not** evidence that the bead is
fabricated. `fe-bjz` is real. It lives in the **federation (city) store**, not
in the gascity rig store:

| | Federation store | gascity rig store |
| --- | --- | --- |
| Beads dir | `/home/jkenkel/indiegems/federation/.beads` | `/home/jkenkel/indiegems/federation/rigs/gascity/.beads` |
| Issue prefix | `fe` | `gcty` |
| Dolt database | `hq` | `gcty` |
| Dolt server | `127.0.0.1:49236` | `127.0.0.1:49236` |
| Sync remote | `https://doltremoteapi.dolthub.com/jkenkel-indiegems/federation-1` | `git+https://github.com/nonathaj/gascity.git` |

Both databases are served by the *same* Dolt server on port `49236`, which is
why the port file is identical in both rigs and why "is the server up" is never
the right question here. The two stores are different **databases**, not
different servers.

Every GC agent session exports `BEADS_DIR` pinned at its own rig:

```
BEADS_DIR=/home/jkenkel/indiegems/federation/rigs/gascity/.beads
```

`bd` honors `BEADS_DIR` ahead of directory discovery, so inside a gascity
session `bd` talks to the `gcty` database **no matter which directory you `cd`
into** — including the federation root itself. To reach `fe-bjz` you must
override the pin explicitly:

```bash
# either form works from anywhere
bd -C "$GC_CITY" show fe-bjz
BEADS_DIR=/home/jkenkel/indiegems/federation/.beads bd show fe-bjz
```

Anything with an `fe-` prefix — including session ids and vapor wisps — resolves
only that way.

## The Bead, As It Actually Closed

`fe-bjz` is a **vapor wisp** (`gc.kind=wisp`) for the formula
`mol-dog-stale-db`, routed to `bd.dog`, labeled `order-run:mol-dog-stale-db`.
It ran once, normally, and closed cleanly:

| Field | Value |
| --- | --- |
| `created_at` | `2026-08-27T00:00:32Z` |
| `started_at` | `2026-08-27T20:01:03Z` |
| `closed_at` | `2026-08-27T20:01:53Z` |
| `close_reason` | `Stale DB scan complete (orphans=0, applied=0, escalated=0)` |
| `assignee` | `bd__dog-1-pool` |
| `status` | `closed` |

The scan it performed found nothing to do: zero orphans, zero bytes reclaimed,
zero errors. There was never anything wrong with the 2026-08-27 run.

`bd__dog-1-pool` appears above strictly as **recorded ledger data**. It is the
assignee string that the closing session wrote. Nothing in Gas City may branch
on that value: no code, test, or config may special-case it, and this page is
not a licence to introduce one.

## The Note Dated 2026-09-09

`fe-bjz` now carries two scan notes:

| Note heading | What it is |
| --- | --- |
| `## scan (dry-run) 2026-08-27T20:01:52Z` | Legitimate. Written one second before the bead closed. |
| `## scan (dry-run) 2026-09-09T04:04:48Z` | **Spurious.** Written 12 days 8 hours after the bead closed. |

The bead's `updated_at` is `2026-09-09T04:04:49Z` — one second after the second
note's own timestamp. That is the write landing. A closed row accepted an append
**13 calendar days** (12d 8h 2m 55s) after it closed, because nothing on the
write path refuses note writes to closed beads.

### It is a real scan, not a replayed copy

This matters, because "the same note got duplicated" would be a much smaller
problem than what actually happened. The two notes disagree in ways only a fresh
execution can produce:

| | 2026-08-27 note | 2026-09-09 note |
| --- | --- | --- |
| `rigs_protected` | 12 databases | **14** databases |
| Extra entries | — | `gf`, `gcty` |
| `reaped.protected_pids` | `[120313, 3566566, 4127411]` | `[120313, 2548116]` |

The `gascity` (`gcty`) and `gem-federation` (`gf`) rigs were registered *after*
`fe-bjz` closed, and the live pid set had turned over. So a dog session really
did run a scan on the night of 2026-09-09 — and then appended its perfectly good
output to a row that had been closed since August, because that was the bead the
claim path had handed it.

The scan result itself was clean again: 0 orphans, 0 bytes, 0 errors. **No
maintenance action was lost or wrongly taken.** The damage was in the churn, not
in the scan.

## The Loop That Produced It

The controller's demand side and the claim side disagreed about whether `fe-bjz`
was live work, and neither consulted store truth before acting:

1. A projection/routing entry still listed `fe-bjz` as claimable.
2. `gc hook --claim` served it — as an *existing assignment*, with no canonical
   re-read of status against the authoritative store.
3. The session took the work, ran the scan, and tried to close the bead.
   `bd close` refused: the row was already closed, in a different store, under a
   different assignee.
4. The session drained with work still assigned to it
   (`session.drain_acked_with_assigned_work`).
5. The entry was never invalidated, so the next tick re-armed and re-served the
   same row. Go to 1.

The system diagnosed itself correctly at `2026-09-09T00:08:06-04:00`, in a
`mol-dog-stale-db.escalate` event:

> work-bead identity fault: hook leased `fe-bjz` (in_progress,
> assignee=`fe-eov5gu`/`bd__dog-2-pool`) but city store has `fe-bjz` closed since
> 2026-08-27 with assignee=`bd__dog-1-pool`, so `bd close` refuses;
> `GC_TRIGGER_BEAD_ID` exported `fe-v70hln` (tonight's open unclaimed wisp) which
> the formula would have closed unworked. Scan itself clean: 0 orphans, 0 bytes,
> 0 errors.

Two further facts from that message are worth keeping:

- The hook believed `fe-bjz` was `in_progress` under a **third** identity
  (`bd__dog-2-pool`, session `fe-eov5gu`) while the store held it closed under
  the original one. The row's identity was spelled differently on each side.
- The formula was one step away from closing `fe-v70hln` — that night's genuine,
  never-started wisp — as if it were the work it had just done. The correct wisp
  was in scope only as collateral.

At `2026-09-09T02:46:10-04:00` the dead-assignee path fired once and
*re-armed* the loop rather than ending it:

> reopened routed work `fe-bjz` assigned to dead session `fe-eov5gu` (route
> `bd.dog`); assignee cleared so the pool can reclaim it

## What It Cost

Measured from the city event log
(`/home/jkenkel/indiegems/federation/.gc/events.jsonl`, covering
`2026-09-08T09:59:01-04:00` → `2026-09-09T04:33:55-04:00`):

| Signal | Measured |
| --- | --- |
| `session.drain_acked_with_assigned_work` (00:00–02:59 EDT) | **73** (21 + 28 + 24 by hour) |
| `session.drain_acked_with_assigned_work` (peak 5h window) | 74 |
| `session.drain_acked_with_assigned_work` (measured window) | 95 — of which 72 name `fe-bjz` |
| `fe-bjz` events, peak 60-minute window (01:46:43 → 02:46:10 EDT) | **235** |
| `fe-bjz` events, measured window | 803 (587 `bead.updated`, 53 `bead.created`, 39 `fed.zombie_demand.detected`, 38 `order.failed`) |
| `session.woke` / `session.stopped` (20:00 → 05:00 EDT) | 880 / 885 |
| `session.demand_claim_divergence` (20:00 → 05:00 EDT) | 827 |
| `session.demand_claim_divergence` (measured window) | 1689 |

These corroborate the figures the build was scoped against — roughly **937
wasted sessions attributed overnight**, 73 drain-acks in five hours, and 223
`fe-bjz` events inside an hour. The small differences are windowing: the event
log rotated at `2026-09-08T13:58:28Z`, so part of the overnight accounting sits
in `events.jsonl.archive-*.gz`. Order of magnitude and shape agree.

The 587 `bead.updated` events on a single closed row are the loop's signature:
the row was rewritten roughly every few seconds for hours, to no effect.

### The starved wisp

The real cost was not the wasted sessions but the work that did not run.
`fe-v70hln` was that night's genuine `mol-dog-stale-db` wisp, same formula, same
`bd.dog` route:

| Field | Value |
| --- | --- |
| `created_at` | `2026-09-09T04:01:01Z` (00:01:01 EDT) |
| `started_at` | **`null` — it was never started** |
| `closed_at` | `2026-09-09T06:53:57Z` (02:53:57 EDT) |
| `close_reason` | `Stale DB scan complete (orphans=0, applied=0, escalated=0)` |

It sat unclaimed for **2h 52m 56s** while the pool spent ~880 session wake/stop
cycles on a row that had been closed since August, and it was ultimately closed
with a completion reason despite `started_at` never being set. It was created
3m 47s *before* the misdirected note landed on `fe-bjz`; the loop was already
running when real work arrived, and real work lost.

## Timeline

All times EDT (`-04:00`); ledger fields are quoted in UTC as stored.

| Time | Event |
| --- | --- |
| 2026-08-27 16:01:53 (`20:01:53Z`) | `fe-bjz` closes normally, scan clean. |
| … 12 days pass … | |
| 2026-09-08 19:36:59 | First `bead.closed` on `fe-bjz` in this log — a close attempt against an already-closed row. |
| 2026-09-09 00:01:01 (`04:01:01Z`) | Genuine wisp `fe-v70hln` created, open, routed `bd.dog`. |
| 2026-09-09 00:04:48 (`04:04:48Z`) | **Spurious scan note appended to `fe-bjz`.** |
| 2026-09-09 00:04:49 (`04:04:49Z`) | `fe-bjz.updated_at` bumped — the write landed on the closed row. |
| 2026-09-09 00:08:06 | `mol-dog-stale-db.escalate` names the identity fault exactly. |
| 2026-09-09 00:00–02:59 | 73 `session.drain_acked_with_assigned_work`. |
| 2026-09-09 01:46–02:46 | Peak burst: 235 `fe-bjz` events in 60 minutes. |
| 2026-09-09 02:46:10 | `bead.dead_assignee_reopened` clears the assignee and re-arms the loop. |
| 2026-09-09 02:53:57 (`06:53:57Z`) | `fe-v70hln` closed, `started_at` still null — never worked. |

## What Was Deliberately Not Done

- The `2026-09-09T04:04:48Z` note was **not** deleted, edited, or moved.
- `fe-bjz` was **not** reopened, re-closed, or re-dated to hide the churn.
- The 803 `fe-bjz` events were **not** pruned from the event log.
- No code branches on `bd__dog-1-pool`, `bd__dog-2-pool`, or `fe-bjz`. The fix
  for this class is a store-truth liveness check applied to every candidate, not
  a special case for the row that happened to be loudest.

Retroactive accounting of the wasted sessions is explicitly out of scope. The
number motivated the work; reconciling it is not a deliverable.

## Verifying This Record

Nothing here needs to be taken on trust:

```bash
# the bead, in the store that actually holds it
bd -C "$GC_CITY" show fe-bjz --json

# both notes, still present, still two
bd -C "$GC_CITY" show fe-bjz --json \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); d=d[0] if isinstance(d,list) else d; print(d["notes"])'

# the starved wisp, started_at null
bd -C "$GC_CITY" show fe-v70hln --json
```

The city event log is **append-only and live**, so unscoped counts drift upward
and will not match the table above. Reproduce the measured figures by bounding
the window and matching on the event `type` rather than on a raw substring — the
string `session.drain_acked_with_assigned_work` also appears inside other
events' payloads, so a bare `grep -c` over-counts it by roughly 15%:

```bash
python3 - <<'EOF'
import json, collections
LO, HI = "2026-09-08T09:59:01", "2026-09-09T04:33:56"
rows = [json.loads(l) for l in
        open("/home/jkenkel/indiegems/federation/.gc/events.jsonl",
             encoding="utf-8", errors="replace") if l.strip()]
w = [d for d in rows if LO <= d.get("ts", "") <= HI]
c = collections.Counter(d.get("type") for d in w)
print("events in window            :", len(w))          # 105133
print("fe-bjz events               :", sum("fe-bjz" in json.dumps(d) for d in w))  # 803
print("drain_acked_with_assigned_work:", c["session.drain_acked_with_assigned_work"])  # 95
print("demand_claim_divergence     :", c["session.demand_claim_divergence"])  # 1689
EOF
```

Counts before `2026-09-08T13:58:28Z` live in `events.jsonl.archive-*.gz`.

As of this write-up the `notes` field is 2052 bytes, SHA-256
`31cd5ced76ad7d81b4a612b6f0e525b5ec6f48f21381fe880fa76a74304dd5c2`, containing
exactly two `## scan (dry-run)` blocks, with `updated_at` still
`2026-09-09T04:04:49Z`. If a future reader finds one block, or a later
`updated_at`, the note was altered against the explicit constraint recorded here.

## Related

- The claim-identity mismatch seen in the escalate message — one identity
  spelled two ways across the claim path — is tracked separately as `gcty-suj`,
  "(BUG) Claim identity is session-id vs session-name: `gc hook --claim` stamps
  the id, `bd`'s actor check and the worker predicate expect the name — breaks
  claim verification AND `bd close`". Its operational twin is `gcty-jeoc`,
  "Role-worker claim block spins forever: assignee is session id, `BEADS_ACTOR`
  is session name". Both are must-not-regress for this build, not deliverables
  of it — and both were hit first-hand while producing this record: the claim
  block for this very step rejected its own correctly-assigned bead until the
  identity comparison was widened to accept the session id.
- [Reconciler Debugging](reconciler-debugging.md) for collecting `gc trace`
  artifacts on controller and session-reconciler incidents.
- [Dolt Maintenance](dolt-maintenance.md) for what `mol-dog-stale-db` actually
  does when it runs correctly.

Traceability: REQ-007, AC-4 (plan work item W7).
