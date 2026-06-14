# Release Gate: docs-CI hardening

- Deploy bead: `ga-jfva11`
- Source build bead: `ga-y7r05r`
- Review bead: `ga-9r4k04`
- Source branch: `builder/ga-y7r05r`
- PR: https://github.com/gastownhall/gascity/pull/3504
- Base tip checked: `origin/main` at `e02391fe7f0b4281cd10afc4f9293412467f529d`
- Branch merge base: `a52bb3626da330c3ef2718a48a448751432753d1`
- Source commit checked: `8b713e4f65ac90034b078c697cc54aa5f767f4be`
- Manifest note: `docs/PROJECT_MANIFEST.md` is not present in this checkout, so the deployer release criteria from the handoff prompt were used.

## Gate Criteria

| # | Criterion | Verdict | Evidence |
|---|-----------|---------|----------|
| 1 | Review PASS present | PASS | Review bead `ga-9r4k04` is closed and notes `Review verdict: PASS` for source commit `8b713e4f65ac90034b078c697cc54aa5f767f4be`. |
| 2 | Acceptance criteria met | PASS | See acceptance evidence below. |
| 3 | Tests pass | PASS | `make check-docs`, `go test ./test/docsync`, negative `TestLocalMarkdownLinks` smoke, `BASE_REF=origin/main .github/scripts/docs-render-check.sh origin/main`, `make test`, and `go vet ./...` all completed as expected. PR #3504 status checks were rechecked and all non-skipped checks were successful. |
| 4 | No high-severity review findings open | PASS | Review notes list only low/info follow-ups; unresolved HIGH findings count is 0. |
| 5 | Final branch is clean | PASS | Gate ran in a clean detached worktree; after the gate commit, deployer rechecked `git status --short --branch` before push. |
| 6 | Branch diverges cleanly from main | PASS | PR #3504 reported `mergeStateStatus: CLEAN`, and `git merge-tree --write-tree origin/main HEAD` exited 0 against the current `origin/main` tip with no conflicts. The branch was not rebased from the deployer seat. |
| 7 | Single feature theme | PASS | Commit set is one docs-safety theme: docsync link validation, rendered-docs CI, docs ownership, and contributor guidance. |

## Acceptance Evidence

- `test/docsync` now checks Mintlify-published docs pages for internal page links ending in `.md` or `.mdx` after stripping anchors and query strings.
- A negative smoke edit changing `docs/tutorials/index.md` to link to `/tutorials/03-sessions.md` made `go test -run TestLocalMarkdownLinks ./test/docsync` fail with the expected broken-link report.
- `.github/workflows/docs-render.yml` is scoped to docs PRs, skips drafts, uses read-only contents permission, cancels in-progress reruns for the same PR, and pins `actions/checkout` and `actions/setup-node` by SHA.
- `.github/scripts/docs-render-check.sh` runs Mintlify broken-link checking against `origin/main`, ignores non-page static assets, and fails only on net-new page-link regressions versus the base branch.
- `.github/CODEOWNERS` assigns `/docs/` to `@csells`.
- `CONTRIBUTING.md` describes `make check-docs` as validating local links and Mintlify link hygiene.
- `.github/pull_request_template.md` reminds contributors that docs links should use Mintlify routes, not GitHub markdown paths.
- `grep -R "docs\\.gascity\\.com" -n .` returned no matches.

## Test Evidence

- `make check-docs`: PASS (`ok github.com/gastownhall/gascity/test/docsync 1.765s`).
- `go test ./test/docsync`: PASS (`ok github.com/gastownhall/gascity/test/docsync 1.760s`).
- `go test -run TestLocalMarkdownLinks ./test/docsync` with an injected `/tutorials/03-sessions.md` link: failed as expected with `broken local markdown links`.
- `BASE_REF=origin/main .github/scripts/docs-render-check.sh origin/main`: PASS; Mintlify returned non-zero, but the script found no page-link regressions and exited 0.
- `make test`: PASS (`observable go test: PASS log=/tmp/gascity-test.jsonl.TPImbN`).
- `go vet ./...`: PASS.
- GitHub checks on PR #3504: all non-skipped checks successful, including CI required, pack compatibility gate, CodeQL, dashboard SPA, preflight, worker core, and integration shards.
