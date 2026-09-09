---
schema: gc.build.implementation-summary.v1
workflow:
  id: gcty-nmxk
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
    - path: beads/gcty-tx4v
      hash: bead:gcty-tx4v
      ids:
        - REQ-003
        - REQ-010
        - AC-2
    - path: internal/beadmeta/liveness.go
      hash: sha256:dcea65c2cc2f04aa0c295b4d77f6d6cc2da54f24d7b547482c98c6318fd6e1f8
    - path: internal/beadmeta/liveness_test.go
      hash: sha256:91f1ea292b00f64c810de4d1f644e56067c1810dd38cb6c9868bfabddd670c1e
    - path: internal/config/workquery.go
      hash: sha256:885c243c7ff93eaf94dc63d0581a5ff21197791f9ba70b825c1162ef62d015c7
    - path: internal/config/workquery_liveness_test.go
      hash: sha256:05a8572e341ec4d2f8cf145d270123ce7481dbf44f1e1f244946a6d293d299f1
    - path: internal/config/workquery.go
      hash: git:b338aea1ffbb8bad6928b9be28eed205fdf08fc3
  coverage:
    - id: REQ-003
      status: covered
    - id: REQ-010
      status: covered
    - id: AC-2
      status: deferred
---

# W4: one liveness predicate shared by work_query and scale_check

| ID | Status |
| --- | --- |
| REQ-003 | covered |
| REQ-010 | covered |
| AC-2 | deferred |

## Summary

`scale_check` counted a closed row as demand, so the pool spawned a session
for work that could never be claimed. The session drained roughly two minutes
later and the genuine routed bead behind it kept waiting. REQ-003 explicitly
accepts "never spawning the session in the first place", and that is the fix
taken here: the wasted spawn is removed rather than made cheaper.

`internal/config/workquery.go` already named this hazard class in its header
comment — the "scale_check <-> work_query protocol-mismatch" class, where the
query that decides *whether to spawn* and the query that decides *what to
claim* disagree. The two consumers already shared their `bd` predicate
builders, but not their notion of liveness. Each tier expressed it
differently: the canonical and migration tiers left it implicit in `bd ready`
(which a stale ready projection can violate), the legacy ephemeral tier
spelled it `status=open`, and nothing re-checked the status of a returned row.

This adds one definition — `beadmeta.IsLiveStatus`, with
`beadmeta.LivenessJQSelect()` as its shell rendering — and routes every tier
of both consumers through it. One definition, two consumers, structurally
unable to diverge.

## Intended Behavior

- A row whose store status is `closed` is not routed work and is not demand.
  Both `EffectiveWorkQuery()` and `EffectivePoolDemandQuery()` drop it, so a
  stale ready projection cannot spawn a session for it.
- A live row is unaffected: `open` and `in_progress` both stay visible, and a
  live row sitting behind a closed one in the same batch is still served and
  still counted.
- Liveness is a **deny-list over the one terminal status**, not an allow-list
  of live ones. Two consequences are deliberate: `in_progress` keeps flowing so
  the adoption door can serve a bead back to the session already holding it,
  and a status this build has never heard of stays visible rather than being
  silently starved.
- An **absent** status field is live. A missing field is not evidence of
  closure, and treating it as dead would starve any store or fixture that omits
  it. This is fail-open on the *unknown* axis and fail-closed on the
  *known-dead* axis; it is distinct from W2's fail-open/fail-closed decision,
  which concerns a store read that *errors*, not a status that is unrecognized.
- `beadmeta` was chosen as the home because it is the leaf package both sides
  already import. That also leaves W2 (`gcty-l52m`) a seam with no new
  dependency edge: `cmd/gc/cmd_hook_claim.go` imports `beadmeta` today, so its
  two claim admission doors can consume the same `IsLiveStatus` without any
  further plumbing.

### Fork cost

Tier 2 sh charges per `fork()`, not per shell (see
`engdocs/contributors/windows-portability.md`). The filter was placed to add
none where possible:

- the scale_check count-form folds it into the terminal `jq -s` it already
  runs — zero added forks;
- the migration and legacy-ephemeral tiers fold it into jq stages they already
  run — zero added forks;
- only the canonical first-row tier gains a `jq`, and it sits behind the
  existing non-empty guard, so the common "no demand" tick adds no fork at all.

### Ownership and rollback (review finding F9)

| File | Ownership |
| --- | --- |
| `internal/config/workquery.go` | upstream-owned |
| `internal/config/testdata/workquery/*.golden` | upstream-owned |
| `internal/beadmeta/liveness.go` | new file (fork-added) |
| `internal/beadmeta/liveness_test.go` | new file (fork-added) |
| `internal/config/workquery_liveness_test.go` | new file (fork-added) |

The edit to upstream-owned code is deliberately small and rebasable: four call
sites routed through one new helper pair, with the rule itself in a new file.

**Rollback:** `git revert b338aea1ffbb8bad6928b9be28eed205fdf08fc3`. It
restores the previous implicit-liveness behavior. There is no schema change, no
persisted state change, and no migration to undo — the change only affects the
text of generated shell queries, which are rebuilt from config on every read.

## Changed Files

| File | Change |
| --- | --- |
| `internal/beadmeta/liveness.go` | new: `StatusOpen`/`StatusInProgress`/`StatusClosed`, `IsLiveStatus`, `LivenessJQSelect` |
| `internal/beadmeta/liveness_test.go` | new: predicate contract, including in_progress, case folding, and absent status |
| `internal/config/workquery.go` | `livenessMapJQExpr`/`livenessMapJQCommand`; filter wired into the canonical, migration and legacy-ephemeral tiers and into the count-form |
| `internal/config/workquery_liveness_test.go` | new: shared-predicate guard, closed-row regression, mixed-batch starvation shape, bd-failure guard |
| `internal/config/testdata/workquery/*.golden` (21 files) | regenerated |

The golden churn was verified mechanically rather than by eye: every distinct
inserted segment across all 21 files is the liveness filter, and the only
removed segment is a single `"` from the count-form's terminal `jq` switching
to single quotes so the embedded filter's quotes stay data.

## Verification

Both commands were run from the item worktree
`/home/jkenkel/indiegems/federation/rigs/gascity/worktrees/gcty-tx4v`.

**First verification command** (the new tests, run against the unfixed tree
first and observed failing, then against the fix):

```
go test ./internal/config/ -run TestLiveness -v
```

- Before the fix: FAIL — `undefined: beadmeta.LivenessJQSelect` (build failure,
  the TDD red step). After the predicate landed but before the query wiring,
  the four behavioral cases failed on their assertions.
- After the fix: **PASS** — all 6 cases.

**Final proof command:**

```
go test -count=1 ./internal/config/ ./internal/beadmeta/
go test ./cmd/gc/ -run 'WorkQuery|PoolDemand|ScaleCheck|HookClaim|RoutedPool'
```

- `internal/config` — **PASS** (`ok ... 6.637s`), including the pre-existing
  correspondence tests `TestPoolDemandPredicateSharedWithWorkQuery`,
  `TestPoolDemandAndWorkQueryAgreeOnRoutedSemantics`,
  `TestEffectiveScaleCheckUsesReadyOnly`, and the 21 regenerated goldens.
- `internal/beadmeta` — **PASS** (`ok ... 1.080s`).
- `cmd/gc` consumers — **PASS** (`ok ... 9.417s`).

Also run and passing: `go test ./internal/graphroute/ ./internal/materialize/
./internal/beads/` (the other packages that consume these queries), and
`go vet` clean on all touched packages.

A full `go test ./cmd/gc/` sweep was also run. It reported one failure,
`TestCityRuntimeReloadRestartsConfigWatcherWithNewPackTargets`, which is **not
caused by this change**: it fails on `gc start: config watcher: too many open
files`, because the host's `fs.inotify.max_user_instances` is 128 while the
user held 131 inotify instances across 76 concurrent agent processes. It
reproduces 3/3 in isolation on this host and is already tracked by `gcty-r7fg`
and `gcty-qa2i`; a sibling worker on another item hit the same test
independently. The test does not touch work-query or scale-check generation.
Raising the sysctl needs root and was not attempted here. The `.githooks/pre-commit` hook ran for
the staged change: lint reported 0 issues and `go vet ./...` passed.

One regression was caught and fixed during verification: piping the canonical
tier through `jq` reformatted its output, because `jq` pretty-prints by
default. `livenessMapJQCommand` uses `jq -c` so the tier's output stays
compact, which downstream readers depend on when comparing against `"[]"`.

## Remaining Risks

- **AC-2 is marked `deferred`, not `covered`, and this is the main caveat.**
  W4's acceptance names W1B's integration starvation test
  (`test/integration/hook_claim_starvation_test.go`, `gcty-57o`) as its proof.
  That test cannot pass on W4 alone and was not run here: it lives in W1B's own
  worktree, is not on this branch, and it reproduces the *claim admission* half
  of the defect — `hookCandidateClaimable` admitting a closed row and
  `bd update --claim` flipping it to in_progress. That door is W2's scope
  (`gcty-l52m`, still open). W4 removes the *spawn* half: demand no longer
  counts a closed row, so the session is never spawned. The end-to-end proof
  belongs to the integration bead `gcty-1dn0`, where W2, W3B and W4 meet.
  The starvation *shape* is covered here at unit tier by
  `TestLivenessMixedBatchKeepsOnlyTheLiveRow`.
- The bead's description says to "export W2's liveness predicate", but W2 was
  still open when this ran, so no predicate existed to export. This work
  therefore **defines** it, in the shared leaf both W2's doors and W4's queries
  import, and W2 should consume `beadmeta.IsLiveStatus` rather than writing a
  second one. If W2 lands its own predicate independently, the two must be
  reconciled into one before the integration bead closes — otherwise this
  build reintroduces the exact divergence it exists to remove.
- The canonical work_query tier now depends on `jq`, which it did not before.
  This does not widen the effective environment contract: the migration tier
  and the entire count-form already require `jq` unconditionally, so a `jq`-less
  environment was already unable to detect demand. A `jq` failure in the new
  tier is swallowed by `2>/dev/null`, matching the surrounding tiers, and falls
  through to the next tier rather than surfacing.
- Liveness is enforced at query-generation time, on rows the store already
  returned. It narrows what a stale projection can propagate; it is not a
  store-truth re-read. Only W2's canonical re-read at the admission doors makes
  the claim path authoritative.
- Per-claim latency budget: unchanged by this work. W4 adds no store round
  trip — the filter runs over rows already fetched. The added cost is at most
  one `jq` fork per work_query tick that has candidates. W2 owns the latency
  budget for the round trip it adds.
