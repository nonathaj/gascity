---
schema: gc.build.implementation-summary.v1
workflow:
  id: gcty-q30l
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
    - path: beads/gcty-57o
      hash: bead:gcty-57o
      ids:
        - REQ-010
        - AC-2
    - path: plans/claim-loop-fe-bjz/build/requirements.md
      hash: sha256:f051caa100da13999e466d25a638872d3e5c75ec802bb06e9ef2442921e677af
    - path: plans/claim-loop-fe-bjz/build/implementation-plan.md
      hash: sha256:9b9bce9b50e8f71506986563f0f5c1cf62abb3d3bf62a64e9658e07a9dca8993
    - path: plans/claim-loop-fe-bjz/build/plan-review-report.md
      hash: sha256:5336d268eea4e3223aea0ca53af3ad84462fc3d589a010986775a1a72507aaa6
    - path: test/integration/hook_claim_starvation_test.go
      hash: sha256:1578ff2a218b3cfa0a3eac3d6f539a00d195268ac493acbd104fd8a99e4d0e54
    - path: test/integration/filebdshim/main.go
      hash: sha256:bf6e01c207d2403a10e3637cdc0859baf17b346b225c0ae9eaa6ec180f71ecde
  coverage:
    - id: REQ-010
      status: covered
    - id: AC-2
      status: covered
---

# Implementation Summary — W1B: Integration-tier starvation test

## Summary

Added `TestIntegration_HookClaimStarvation_GenuineBeadWinsOverUnclaimable` in
`test/integration/hook_claim_starvation_test.go`, behind `//go:build
integration`. It places a genuine routed bead and an unclaimable one in the
same work-query window and asserts the genuine bead is claimed **and worked**
by a live pooled session, and that the unclaimable one does not consume the
spawn budget.

The test lives at the integration tier rather than beside the
`hookClaimOps` / `hookClaimOptions` unit seams, per plan-review finding **F6**:
AC-2 requires the genuine bead be "claimed and worked", and "worked" implies a
live session executing, which is not observable at that seam. Here a real
session is spawned by a `max=1` pool, runs the real `gc hook --claim`, and
records what it received.

This item is **test-only**. It deliberately implements no product fix: the
liveness predicate belongs to W4 (`gcty-tx4v`), which is a different work item
and a different owner's boundary.

Source anchor `gcty-57o`; committed in worktree
`/home/jkenkel/indiegems/federation/rigs/gascity/worktrees/gcty-57o` as
`2bea41c31`.

### Coverage

| ID | Status |
| --- | --- |
| REQ-010 | covered |
| AC-2 | covered |

AC-2 has two halves. This item delivers the starvation half ("a genuine routed
bead present in the same window is claimed and worked"). The re-arm half ("the
bead is absent from the claimable set on the following tick") is W1 test 2,
owned by `gcty-to0`.

## Intended Behavior

The test seeds two rows in the integration file bead store, both routed at the
pooled agent:

1. **Unclaimable** — its store row is CLOSED.
2. **Genuine** — open and unassigned.

The work query then serves a *stale projection* in which both rows are
advertised as claimable (status `open`, unassigned, route-matched), with the
closed one first. That is AC-1's seeding recipe and the real `fe-bjz` shape: a
projection that disagrees with the store.

Against unfixed code the failure is deterministic. `hookCandidateClaimable`
(`cmd/gc/cmd_hook_claim.go:289`) admits the closed candidate — it checks only
id, empty assignee and route, never liveness. `ops.Claim` then *succeeds*,
because `bd update --claim` has no liveness check and flips a closed row to
`in_progress`. `claimFirstEligibleHookCandidate` returns terminal on that first
success, so the genuine bead later in the same window is never reached. The
session spawns, cannot act on a closed bead, and drains — the ~2-minute
spawn/drain cycle reported in `gcty-5e1`, with the single pool slot consumed.

Once a W4-shaped liveness predicate gates admission, the closed row is not
admitted, the loop falls through to the genuine bead, and the session claims
and works it.

The test's two assertions map directly onto REQ-010:

- The session's recorded claim names the **genuine** bead, with `action=work`
  and a `worked` marker written only after the live session executed past the
  claim.
- The unclaimable bead's store row is still `closed` afterwards, proving it was
  not claimed and did not consume the spawn budget.

## Changed Files

| File | Change |
| --- | --- |
| `test/integration/hook_claim_starvation_test.go` | **New.** The W1B test, its work-query/agent scripts, seeding and assertion helpers, and the focused proof command in the header comment. |
| `test/integration/filebdshim/main.go` | Fidelity fix (see below). `show --json` now emits a one-element array. Human-readable `show` output unchanged. |

### Incidental fix: `filebdshim` `show --json` shape

The test surfaced a pre-existing bug in the integration bd shim. Real `bd show
--json` emits a **one-element array**, and `beads.BdStore.Get`
(`internal/beads/bdstore.go:1085`) unmarshals strictly into `[]bdIssue`. The
shim encoded a bare object, so every shim-backed `Get` failed with:

    bd show: parsing JSON: json: cannot unmarshal object into Go value of
    type []beads.bdIssue

This blocked the claim path's canonical readback and would break any
integration test whose code path calls `BdStore.Get` against the shim. It was
fixed rather than worked around, per the standing project instruction to fix
test-infrastructure defects on sight. The change is confined to the `--json`
branch of `show`; `bd update --claim --json` was already tolerated by
`parseIssuesTolerant`, so no other shim command needed changing.

## Verification

All commands run from the worktree
`/home/jkenkel/indiegems/federation/rigs/gascity/worktrees/gcty-57o`
(`pwd -P` verified before any source read, edit, test or commit).

**First verification command** — package builds and vets under the integration
tag:

    go vet -tags integration ./test/integration/

Observed: **PASS** (clean, no output).

**Named focused proof command** (also recorded in the test file header):

    go test -tags integration -count=1 -timeout 10m \
        -run TestIntegration_HookClaimStarvation_GenuineBeadWinsOverUnclaimable \
        ./test/integration/

Observed against unfixed code: **FAIL, by design** — this is the required
reproduction:

    --- FAIL: TestIntegration_HookClaimStarvation_GenuineBeadWinsOverUnclaimable (11.40s)
        genuine routed bead gc-7 was STARVED: the session claimed the
        unclaimable closed row gc-6 instead.
        claim result: {Action:work BeadID:gc-6 Reason:claimed Worked:true}

`Worked:true` with `Action:work` confirms a live session executed the real
claim path and was handed the closed row — the live-session evidence F6 asked
for.

**Fix-sensitivity check.** To confirm the bead's second acceptance criterion
("test passes once W4 lands") without implementing W4, a throwaway liveness
predicate of W4's shape was applied locally in
`claimFirstEligibleHookCandidate` — skip a candidate whose store row reads
`closed` — and the same proof command was re-run:

    ok  github.com/gastownhall/gascity/test/integration  32.136s

Observed: **PASS**. The probe was then reverted with `git checkout --
cmd/gc/cmd_hook_claim.go` and its absence confirmed by grep; it is **not** part
of commit `2bea41c31`, which contains only the two files listed above.

**Supporting gates:**

| Command | Result |
| --- | --- |
| `go vet ./...` | PASS (clean) |
| `go test ./test/integration/filebdshim/` | PASS — `ok ... 0.057s` |
| `.githooks/pre-commit` (active; `core.hooksPath` = `.githooks`) | PASS — `lint-changed: 0 issues`, `go vet ./...`, generated docs unchanged |

## Remaining Risks

1. **The test is red until W4 lands.** This is the intended state and the
   bead's first acceptance criterion, but it means the integration suite has a
   known failure until `gcty-tx4v` merges. Whoever sequences the merge should
   land W4 before, or together with, this test — otherwise CI is red for
   reasons unrelated to the change under review.

2. **The probe is not the fix.** Fix-sensitivity was demonstrated with a
   liveness predicate placed at the fresh-claim admission point. W4 exports the
   predicate into shared work-query generation so `scale_check` and
   `work_query` cannot diverge. If W4 lands at a different seam than the probe
   used, this test still asserts the right *outcome* but has not pre-validated
   that specific seam.

3. **The unclaimable bead is modeled as a closed store row.** That is the
   `fe-bjz` shape and AC-1's recipe, but REQ-003's "claim that cannot stick"
   class is broader (deleted rows, cross-store-invisible ids, transient write
   failures). Those variants are not covered here; the per-tick skip path for
   claims that *error* is already exercised by the existing `claimsErrored`
   logic and is untouched.

4. **Candidate ordering is fixed by the fixture, not discovered.** The work
   query serves the closed row first from a file, which is what makes the
   starvation deterministic rather than racy. If a future change reorders
   candidates before admission, this test would pass without the underlying
   liveness gap being fixed. The assertion that the closed row is still
   `closed` afterwards limits, but does not fully close, that blind spot.

5. **Harness deviation for this stage's own gate.** The prompt directs running
   `.gc/scripts/checks/build-artifact-valid.sh` from the launcher rig root
   named by the workflow root's `gc.work_dir`. On this workflow root
   (`gcty-q30l`) `gc.work_dir` is **empty**, and
   `/home/jkenkel/indiegems/federation/rigs/gascity/.gc/scripts/checks/` does
   not exist. The validator was therefore run from the resolved pack copy under
   `~/.gc/cache/repos/9522f335.../gascity/assets/scripts/checks/`, whose
   sibling `validate_build_artifact.py` is present. This is the same harness
   gap already recorded as Open Question 6 in `requirements.md`; it is an
   observation about the gate wiring, not a property of this change.
