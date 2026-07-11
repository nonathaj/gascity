# Phase 5 Kickoff Prompt

Paste the block below to start the next session.

---

Continue the gascity feature-flag rollout (PR-S2a) on branch `worktree-reconciler`
at `/data/projects/gascity/.claude/worktrees/reconciler`. Phases 1–4 are complete,
committed, local/UNPUSHED, inert: `bec9156b1` (S2-T1 interface + typed errors +
`Bead.Revision json:"-"`), `ec0bccd04` (S2-T2 conformance harness + S2-T3 MemStore
CAS), `da0d073a6` (S2-T3 FileStore CAS), `6c0160669` (S2-T4/T5 BdStore result
classifier + four-verb capability probe + latch, in `internal/beads/bdstore_conditional.go`).
Read `engdocs/plans/feature-flags/PHASE5-HANDOFF.md` first — it has the full status,
the Phase-4 API surface Phase 5 consumes (classifier/probe/latch + the `Expected`/`ID`
stamping contract), the verified BdStore surface map, the F2 fix analysis, the SQL-spike
task, gates, and gotchas. Build spec: `engdocs/plans/feature-flags/PR-S2a-BUILD-SPEC.md`
(keep its Progress block current). DESIGN detail: `DESIGN.md` §8.2 (retry policy) / §8.4
(emulation + SQL spike).

Now build **Phase 5 = S2-T6 + S2-T7** in `internal/beads/bdstore_conditional.go`
(+ `bdstore_conditional_internal_test.go`), and fix **F2** in the same phase:

1. **The three `*IfMatch` verbs** (`UpdateIfMatch`/`CloseIfMatch`/`DeleteIfMatch`):
   check `conditionalWritesCapable()` (→ `ErrConditionalWriteUnsupported` if not),
   build the unconditional verb's argv + `--if-revision N --json`, run through a NEW
   `runConditionalWrite` wrapper, then `classifyConditionalWriteResult`. On an
   unsupported result call `markConditionalWritesUnsupported()`; on a
   `*PreconditionFailedError` stamp `ID` and **override `Expected` with the caller's
   own argument** (the harness asserts `Expected == the stale revision` unconditionally).
2. **`runConditionalWrite`** — a dedicated retry wrapper that **NEVER** routes through
   `runBDTransientWrite`/`isBdTransientWriteError`: connection/serialization errors
   RE-READ the revision before re-attempt (never replay a stale fence); precondition
   surfaces immediately; ambiguous surfaces as-is; never downgrade to an unconditional
   write. It MUST still apply the doltlite `--dolt-auto-commit off` prefix via
   `s.bdTransientWriteArgs(args)`.
3. **`CompareAndSetMetadataKey`** — bounded emulation loop (§8.4 verbatim:
   `casEmulationMaxAttempts=4`, `casEmulationBaseBackoff=25ms` doubling+jittered):
   Get → value check (`""≡absent`) → `runConditionalWrite` update `--set-metadata`;
   `nil`→`(true,nil)`; precondition→retry; exhaustion→`*CASRetriesExhaustedError`
   (NOT `PreconditionFailedError`, NOT `(false,nil)`); other→`(false,err)`. Add a
   **sleep seam** so tests don't actually sleep.
4. **SQL spike** (§8.4): evaluate the single-`UPDATE` `ReleaseIfCurrent` path; the
   disqualifier is that it must also `revision = revision + 1` atomically — bd #4682
   (the revision column) is unlanded, so the recommended verdict is **emulation ships,
   SQL path dropped**. Record it as a dated note in `engdocs/plans/feature-flags/`.
5. Add `var _ ConditionalWriter = (*BdStore)(nil)`.
6. **F2 (`bd show ga-zj78gu` — read it):** the compile-assert above activates
   promotion through `DoltliteReadStore` (embeds `*BdStore`), which reads revision 0
   via direct SQL → every CAS fails forever in `GC_NATIVE_DOLTLITE_BEADS`. A
   `ConditionalWriterHandle()` alone does NOT help (the direct assertion in
   `ConditionalWriterFor` wins over the provider). **Fix:** define the four CAS methods
   on `*DoltliteReadStore` to loudly degrade — return `ErrConditionalWriteUnsupported`
   — documented as "direct-SQL reads can't supply CAS revisions until #4682 adds the
   column." Audit `internal/beads/exec/exec.go:163` (`beadWire.toBead`) too. Close
   `ga-zj78gu` when done.

Process (non-negotiable): **bounded Fable design pass** (model `fable`; the synthesis
in the main loop or a focused critique — a past agent stalled on oversized output) →
**TDD** (red-first via a scripted white-box runner whose apply-func can mutate fake
backing state, for the committed-but-ambiguous and re-read-on-transient cells) →
**Fable red-team BEFORE the commit** (read-only on the shared worktree; it PROPOSES
mutations, you RUN them to prove teeth — the Phase-4 red-team empirically found 4
surviving mutants + 2 real parse bugs the design critique missed) → full gates (full
`go test ./internal/beads/...` not `-run`, `-race` on Conditional, vet, golangci-lint,
gofumpt, the `OpenAPISpecInSync|EventPayload` wire gate) → commit with trailer
`Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Mutation
discipline: back up to `$CLAUDE_JOB_DIR/tmp`, python string-replace, run subtest, `cp -f`
restore — **NEVER `git checkout`** (wipes the uncommitted stack).

Then **Phase 6** (S2-T8 CachingStore evict-never-patch, the livelock MERGE GATE) and
**PR-S2b** (S2-T10..T12). **Do NOT push. Do NOT start S3** without checking in — S3 is
outward-facing (deploy-lineage sync + the live maintainer-city flip).
