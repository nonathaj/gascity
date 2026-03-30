#!/usr/bin/env bash
# Ralph check script for design review loop (personal work formula).
#
# Reads the design review verdict from bead metadata.
# Exit 0 = pass (stop iterating), exit 1 = fail (retry).
#
# Expected metadata key: design_review.verdict
# Values: "done" | "iterate"

set -euo pipefail

BEAD_ID="${GC_BEAD_ID:-}"
if [ -z "$BEAD_ID" ]; then
    echo "ERROR: GC_BEAD_ID not set" >&2
    exit 1
fi

CURRENT_JSON=$(bd show "$BEAD_ID" --json 2>/dev/null)
LOGICAL_ID=$(printf '%s\n' "$CURRENT_JSON" | jq -r 'if type == "array" then (.[0].metadata["gc.logical_bead_id"] // "") else (.metadata["gc.logical_bead_id"] // "") end')
ROOT_ID=$(printf '%s\n' "$CURRENT_JSON" | jq -r 'if type == "array" then (.[0].metadata["gc.root_bead_id"] // "") else (.metadata["gc.root_bead_id"] // "") end')
RALPH_STEP_ID=$(printf '%s\n' "$CURRENT_JSON" | jq -r 'if type == "array" then (.[0].metadata["gc.ralph_step_id"] // "") else (.metadata["gc.ralph_step_id"] // "") end')
VERDICT=$(printf '%s\n' "$CURRENT_JSON" | jq -r 'if type == "array" then (.[0].metadata["design_review.verdict"] // "") else (.metadata["design_review.verdict"] // "") end')
if [ -n "$LOGICAL_ID" ]; then
    LV=$(bd show "$LOGICAL_ID" --json 2>/dev/null | jq -r 'if type == "array" then (.[0].metadata["design_review.verdict"] // "") else (.metadata["design_review.verdict"] // "") end')
    [ -n "$LV" ] && VERDICT="$LV"
fi
if [ -z "$VERDICT" ] && [ -n "$ROOT_ID" ] && [ -n "$RALPH_STEP_ID" ]; then
    VERDICT=$(bd list --all --metadata-field gc.root_bead_id="$ROOT_ID" --metadata-field gc.ralph_step_id="$RALPH_STEP_ID" --json 2>/dev/null | jq -r 'sort_by(.created_at // .created // "") | reverse | map(.metadata["design_review.verdict"] // empty) | map(select(. != "")) | .[0] // ""')
fi
VERDICT="${VERDICT:-iterate}"

case "$VERDICT" in
    done|approved|pass)
        echo "Design review approved — stopping iteration"
        exit 0
        ;;
    iterate|fail|retry)
        echo "Design review needs iteration — retrying"
        exit 1
        ;;
    *)
        echo "Unknown verdict: $VERDICT — treating as iterate" >&2
        exit 1
        ;;
esac
