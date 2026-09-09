---
schema: gc.build.implementation-summary.v1
workflow:
  id: gcty-9az5
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
    - path: beads/gcty-jmu6
      hash: bead:gcty-jmu6
      ids:
        - REQ-002
        - AC-1
    - path: plans/claim-loop-fe-bjz/build/w3a-attribution.md
      hash: git:1701a8cd3d477cc91d489e6189d0a99185215345
    - path: cmd/gc/session_reconciler.go
      hash: sha256:222ec4aee9777c5f3bd1d85b07275b4e6ac0eb2eb4f99722457517decf685e52
    - path: cmd/gc/session_reconciler_stale_wisp_projection_test.go
      hash: sha256:d1d6876193659878dcb41109e02ea0f32ce14dd4a56835d7ef6204fdb31872c3
    - path: cmd/gc/work_assignment.go
      hash: sha256:7fd0852957736a1ad548c665ad4b5ccb92515c84de05635ebd971f6ec9c91c67
    - path: cmd/gc/work_assignment_test.go
      hash: sha256:f29acb409f0f20cc173591a0305004586a7c0c9666a34a6eee901f84a69f5661
  coverage:
    - id: W3B-1
      status: covered
    - id: W3B-2
      status: covered
    - id: W3B-3
      status: covered
    - id: REQ-002
      status: covered
    - id: AC-1
      status: deferred
      rationale: >-
        AC-1 asserts that `gc hook --claim` returns `action != work` for a closed
        row. That assertion lives at the claim-admission door in
        cmd/gc/cmd_hook_claim.go, which W2 (gcty-l52m) owns and which is still
        open. This bead removes the upstream source of the stale offer — the
        projection entry that produced the candidate — so the claimable set no
        longer contains the closed row. The end-to-end `action != work`
        assertion, and W1's cmd_hook_claim_closed_row_test.go that carries it,
        land with W2.
---

# W3B implementation summary — invalidate the stale wisp projection entry

## Summary

Bead `gcty-jmu6` (W3B) asked for the change half of plan work item W3: invalidate or
correct the stale projection entry that lost to store truth, in the component W3A
named. W3A (`gcty-bsi`) named it precisely — the controller's in-process
`CachingStore`, reached through a *positive-only* wisp probe — and directed W3B at
its sites 3-5, with site 3 as the minimal fix.

The defect was one short-circuit. `sessionHasOpenAssignedWispWork`
(`cmd/gc/session_reconciler.go`) asked the cache first and returned `true` on any
positive answer, never consulting the backing store:

```go
if items, ok := wa.CachedOpenAssignedWisps(assignee, status); ok {
    if wa.HasNonSessionWork(items) {
        return true, nil          // cache says yes -> return, never consult backing
    }
}
return sessionHasOpenAssignedWorkForTier(store, assignee, status, beads.TierWisps, true)
```

Only a cache *negative* reached the authoritative `live=true` read, so a stale positive
was self-sustaining: it could never be contradicted. `CachedList` seals it by refusing
closed-inclusive queries outright, leaving that path structurally unable to observe a
row the store has closed. This is why `fe-bjz` — closed `2026-08-27T20:01:53Z` — was
still served as claimable work 13 days later, and why all 72
`session.drain_acked_with_assigned_work` events reported the cached
`bead_status: in_progress` rather than the persisted `closed`.

The fix deletes the short-circuit, so the probe always reads store truth. This is not a
new mechanism: the live path already reconciles the projection. `CachingStore.List`
with `Live: true` calls `refreshCachedBeads`, which computes `staleLiveCacheIDs` — the
cache rows a live read did not return — re-reads each from the backing store and
absorbs the fresh row (or evicts it when the store reports `ErrNotFound`). So routing
the probe through the live read satisfies both halves of REQ-002 at once: the store row
wins, *and* the stale entry is corrected or invalidated. There is no
`if looks_stale then skip` anywhere in the change — only "the store says this row is
not live, therefore it is not work".

`workAssignment.CachedOpenAssignedWisps` existed solely to serve that short-circuit; its
doc comment described itself as "the typed form of the positive-only cache probe in
sessionHasOpenAssignedWispWork". With the short-circuit gone it is production-dead, and
leaving a ready-made positive-only cache probe beside the fix is an active invitation to
reintroduce the defect, so it and its two dedicated tests are removed. `CachedList`
(W3A's site 5) is deliberately left alone: refusing to serve closed-inclusive queries
from an active-only cache is correct in itself, and it is shared infrastructure with
callers far outside this path. The defect was trusting it positively, not its own
behavior.

**On the second emitter (third acceptance clause).** W3A established by census over the
full event window that **no second `bead.updated status=open` emitter exists**. Exactly
one `status=open` occurred, at `02:46:10.182860`, 184 microseconds before — and
belonging to the same write as — the single `bead.dead_assignee_reopened`. The
"repeating cycle" in the original report was a substring-match artifact (218 lines
*mentioning* `fe-bjz` versus 3 events whose *subject* was `fe-bjz`). Nothing is
silently ignored here and no follow-up bead is needed for a second emitter, because
there is none to split out. The real ~2-minute cadence was the drain-ack series this
change addresses.

## Intended Behavior

- A wisp-tier row that the store reports CLOSED is never reported as open assigned work,
  regardless of what the in-memory projection still holds.
- The probe consults the authoritative store on every call. A projection entry that
  disagrees with the store row is corrected to the store's value, or evicted when the
  row is gone.
- Consequently the row leaves the claimable set: on the following tick it is absent, so
  the offer cannot re-arm and the loop cannot sustain itself.
- No behavior change when cache and store agree, which is the overwhelmingly common
  case: the same set of beads is returned, now read from the store.

## Changed Files

All paths are repo-relative, edited and committed inside the item worktree
`/home/jkenkel/indiegems/federation/rigs/gascity/worktrees/gcty-jmu6`.

| File | Ownership | Change |
| --- | --- | --- |
| `cmd/gc/session_reconciler.go` | upstream-owned | `sessionHasOpenAssignedWispWork` now delegates to the authoritative `live=true` tier read. Doc comment records why no cache fast-path may be reintroduced. |
| `cmd/gc/session_reconciler_stale_wisp_projection_test.go` | fork-owned (new) | Two tests: store truth wins over a stale positive projection; and the entry is absent from the claimable set across two consecutive ticks. |
| `cmd/gc/work_assignment.go` | upstream-owned | Removed `CachedOpenAssignedWisps`, production-dead once the short-circuit is gone. |
| `cmd/gc/work_assignment_test.go` | upstream-owned | Removed the two tests and the `fakeCachingWorkStore` fixture covering the removed helper, plus its clause in the nil-store test. |

The change to upstream-owned code is a body replacement in one function plus the
deletion of one unused method — small and self-contained, so it rebases, drops, or is
proposed upstream cleanly. **Rollback path:** reverting the commit restores the previous
behavior exactly; nothing else depends on the removed helper, and no state, schema, or
wire format changes.

## Verification

All commands were run from inside the item worktree (`pwd -P` confirmed equal to
`work_dir` before any source read, edit, test, hash, or commit).

**First verification command — the reproduction, observed FAILING against unfixed code**
(the tests were written before the fix, per the project's reproduce-first rule):

```
$ go test ./cmd/gc/ -run TestSessionHasOpenAssignedWispWork -v
=== RUN   TestSessionHasOpenAssignedWispWorkStoreTruthWins
    session_reconciler_stale_wisp_projection_test.go:74: closed row served as open assigned wisp work: a positive cache answer was trusted over store truth
--- FAIL: TestSessionHasOpenAssignedWispWorkStoreTruthWins (0.00s)
=== RUN   TestSessionHasOpenAssignedWispWorkReArms
    session_reconciler_stale_wisp_projection_test.go:92: tick n: closed row still reported as claimable work
--- FAIL: TestSessionHasOpenAssignedWispWorkReArms (0.00s)
FAIL
FAIL	github.com/gastownhall/gascity/cmd/gc	0.593s
```

Result: **FAIL**, as required — the reproduction is real, and it fails for the attributed
reason rather than incidentally.

**Final proof command — the same tests after the fix:**

```
$ go test ./cmd/gc/ -run TestSessionHasOpenAssignedWispWork -v
=== RUN   TestSessionHasOpenAssignedWispWorkStoreTruthWins
--- PASS: TestSessionHasOpenAssignedWispWorkStoreTruthWins (0.00s)
=== RUN   TestSessionHasOpenAssignedWispWorkReArms
--- PASS: TestSessionHasOpenAssignedWispWorkReArms (0.00s)
PASS
ok  	github.com/gastownhall/gascity/cmd/gc	0.576s
```

Result: **PASS**.

Supporting runs, all observed passing:

| Command | Result |
| --- | --- |
| `go vet ./cmd/gc/ ./internal/beads/` | PASS (clean, exit 0) |
| `go test ./internal/beads/` | PASS (`ok ... 9.473s`) |
| `go test ./cmd/gc/ -run 'TestCityRuntimeBeadReconcileTick\|TestWorkAssignment\|TestSessionHasOpenAssignedWisp'` | PASS (15/15) |

The repo-wide gate `make test` was also run and **failed**, but not because of this
change. It failed in two ways, both traced to host saturation at the time of the run
(load average 31.5 on 24 cores, nine concurrent `go test` processes, four concurrent
full-repo sweeps from sibling drain workers):

1. `TestControllerReloadsConfig` failed on its 60s deadline. It fails **identically on
   the parent commit `dfb4d5ab5` with this change absent**, so it is pre-existing. Root
   cause is in its captured stderr — `config watcher: too many open files` — i.e.
   `fsnotify.NewWatcher()` returning EMFILE because the host's
   `fs.inotify.max_user_instances` (128) is exhausted (131 instances held). Filed as
   `gcty-f4t2`.
2. Three further watcher tests failed in the sweep but passed in isolation seconds later
   (0.95s / 0.02s / 0.01s) — load-induced flakes, same likely cause.

`cmd/gc` then hit the 15m package timeout with 6721 tests started and 82 unfinished; the
tests added here were never reached in that sweep, and they pass under the focused proof
command above. No failing or hung test in either sweep touches the wisp probe, the work
assignment façade, or the caching store.

The targeted regression set deliberately includes
`TestCityRuntimeBeadReconcileTick_BootDoesNotBlockOnWispSweep`, which pins the
gastownhall/gascity#3288 boot-hang fix on this exact read path (its `wispBlockingStore`
blocks every `TierWisps` read). It still passes: that test's store is not a
`CachingStore`, so the removed probe never answered there, and the boot tick still
defers the sweep.

## Remaining Risks

1. **AC-1 is not closed by this bead alone.** AC-1 asserts `action != work` out of
   `gc hook --claim`; that door is `cmd/gc/cmd_hook_claim.go`, owned by W2
   (`gcty-l52m`), still open. W3A also found that `gc hook --claim` has **no closed-row
   check at any point** — `hookClaimExistingOrAssigned` reads `candidate.Status` off the
   projection and returns `action=work` with no canonical read — so it would re-serve a
   closed row offered by any other work query. This change removes the source that fed
   the observed loop; it does not itself gate the hook. W1's
   `cmd_hook_claim_closed_row_test.go` (commit `006719a7`) carries the end-to-end
   assertion and is not present in this worktree, so it was not run here.
2. **Added store reads on the wisp probe.** A positive cache answer previously returned
   without a store round-trip; now every wisp probe performs the live tier read. The
   added cost is bounded and proportionate: the issues-tier probe in the same loop
   (`sessionHasAssignedWorkInStoreByIdentifiersForStatuses`) *already* ran unconditionally
   with `live=true`, so each iteration already made at least one live read, and the loop
   still short-circuits on the first positive. The worst case is one extra live
   `List(TierWisps)` per candidate × status × identifier where the cache would have
   answered positively. This is the read the #3288 boot-hang test exists to keep off the
   boot path, and it remains off it. Worth watching on a heavy-session city; if it does
   show up, the correct answer is a *negative-safe* cache (one that can observe closed
   rows), not restoring the positive-only short-circuit.
3. **The gascity rig has no materialized `.gc/scripts/`.** The step prompt directs the
   validator to be run as `.gc/scripts/checks/build-artifact-valid.sh` from the launcher
   rig root, but `/home/jkenkel/indiegems/federation/rigs/gascity/.gc/scripts/` does not
   exist at all (peer rigs such as `gem-api` and the federation root do have it). This is
   very likely the same infrastructure fault recorded in W1's close reason, where step
   `gcty-70e5` was quarantined `gc.outcome=fail` for the identical missing script — that
   is, an unmaterialized rig scripts directory rather than a missing validator. The
   validator itself is present in the pack cache at the same repo hash that supplied this
   step's prompt file, and was run from there against this artifact:
   `GC_BEAD_ID=gcty-0ge3 bash <pack-cache>/gascity/assets/scripts/checks/build-artifact-valid.sh`
   → `build artifact valid: schema=gc.build.implementation-summary.v1` (exit 0). The
   artifact is therefore validator-confirmed, but the rig-local invocation path the prompt
   names remains broken and is worth a follow-up bead.
4. **`cmd/gc` cannot currently complete a full-package sweep on this host.** As a
   single monolithic run it exceeds the default 10m timeout, and under `make test`
   (`GC_FAST_UNIT=1`, `-timeout 15m`) it exceeded 15m as well, with 6721 tests started
   and 82 unfinished. The proximate cause is host saturation from concurrent sibling
   drain workers rather than package size alone, and it is compounded by the inotify
   exhaustion in `gcty-f4t2`. This change does not contribute: it removes a cache
   fast-path in one predicate, and for every store that is not a `*beads.CachingStore`
   the probe's behavior is byte-identical to before (the cache type assertion already
   failed, so the live read already ran). Verification here therefore rests on the
   focused proof command, `go vet`, the full `internal/beads` package, and the targeted
   reconciler/work-assignment regression set — all passing.

5. **Test coverage was removed along with the helper.** Two tests and one fixture
   covering `CachedOpenAssignedWisps` were deleted because the function they covered no
   longer exists. The behavior they protected — asserting `CachedList` on the unwrapped
   `.Store` rather than the `WorkStore` wrapper — is still exercised by the remaining
   `workAssignment` tests for the paths that still use the unwrap helper.
6. **Sibling worktrees are unintegrated.** W1's and W3A's commits are not ancestors of
   this worktree's `HEAD` (`dfb4d5ab5`), and the launcher checkout's plan directory was
   observed changing underneath this session while sibling drain members ran. Final
   cross-item verification belongs to the integration step, not here.

## Coverage

| ID | Status | Note |
| --- | --- | --- |
| W3B-1 | covered | Store truth wins: a closed wisp row is no longer reported as open assigned work from a stale positive projection. |
| W3B-2 | covered | Re-arm closed: the stale entry is corrected or evicted by the live read, so the row is absent from the claimable set on the next tick. |
| W3B-3 | covered | Second `status=open` emitter: W3A established by full-window census that none exists; recorded explicitly rather than ignored, so no follow-up bead is required. |
| REQ-002 | covered | The claimable set is reconciled against the store before a claim is served, and the disagreeing cache entry is corrected or invalidated. |
| AC-1 | deferred | Asserts `action != work` at the `gc hook --claim` door, owned by W2 (`gcty-l52m`); this bead removes the stale offer's source. See front-matter rationale. |
