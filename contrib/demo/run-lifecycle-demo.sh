#!/usr/bin/env bash
# run-lifecycle-demo.sh — Top-level orchestrator for the 3-act demo.
#
# All agents are deterministic bash scripts — no Claude API calls,
# fully reproducible, zero cost.
#
# Usage:
#   ./run-lifecycle-demo.sh [act1|act2|act3|all]
#
# Acts:
#   Act 1: Pack Escalation    — wasteland → swarm → lifecycle (manual edits)
#   Act 2: Provider Swap           — same pack, local tmux → Docker containers
#   Act 3: EKS Scale-Up           — 5 agents → 200 agents, one config edit

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
# shellcheck source=narrate.sh
source "$SCRIPT_DIR/narrate.sh"

ACT="${1:-all}"

# ── Cleanup helper ────────────────────────────────────────────────────────

cleanup_between_acts() {
    tmux kill-session -t gc-demo 2>/dev/null || true
    tmux kill-session -t gc-lifecycle 2>/dev/null || true
    tmux kill-session -t gc-provider 2>/dev/null || true
    tmux kill-session -t gc-eks 2>/dev/null || true

    local demo_city="${DEMO_CITY:-$HOME/demo-city}"
    if [ -d "$demo_city" ]; then
        (cd "$demo_city" && gc stop 2>/dev/null) || true
        (cd "$demo_city" && gc daemon stop 2>/dev/null) || true
    fi

    # Stop any Docker containers from act 2.
    docker ps -q --filter label=gc 2>/dev/null | xargs -r docker stop 2>/dev/null || true
    docker ps -aq --filter label=gc 2>/dev/null | xargs -r docker rm -f 2>/dev/null || true
}

# ── Act runners ───────────────────────────────────────────────────────────

run_act1() {
    "$SCRIPT_DIR/act1-pack-escalation.sh"
    cleanup_between_acts
}

run_act2() {
    "$SCRIPT_DIR/act2-provider-swap.sh"
    cleanup_between_acts
}

run_act3() {
    GC_HYPERSCALE_MOCK="${GC_HYPERSCALE_MOCK:-true}" \
        "$SCRIPT_DIR/act3-eks-scaleup.sh" "${2:-5}" "${3:-200}"
    cleanup_between_acts
}

# ── Main ──────────────────────────────────────────────────────────────────

case "$ACT" in
    act1)
        run_act1
        ;;
    act2)
        run_act2
        ;;
    act3)
        run_act3
        ;;
    all)
        narrate "Gas City Lifecycle Demo" --sub "Three acts, one SDK, zero API calls"
        echo "  Act 1: Pack Escalation    — wasteland → swarm → lifecycle"
        echo "  Act 2: Provider Swap          — same pack, local → Docker"
        echo "  Act 3: EKS Scale-Up           — 5 → 200 agents, one config edit"
        echo ""
        pause "Press Enter to begin..."

        # ── Act 1 ──
        run_act1

        narrate "Act 1 done" --sub "Next: Provider Swap"
        pause

        # ── Act 2 ──
        run_act2

        narrate "Act 2 done" --sub "Next: EKS Scale-Up"
        pause

        # ── Act 3 ──
        run_act3

        # ── Finale ──
        narrate "Demo Complete" --sub "Gas City: orchestration as configuration"
        echo "  Three capabilities demonstrated:"
        echo "    1. Pack escalation    — wasteland → swarm → lifecycle"
        echo "    2. Provider swap          — local → Docker, same pack"
        echo "    3. EKS scale-up           — 5 → 200 agents, one edit"
        echo ""
        echo "  Same beads. Same daemon. Configuration changes only."
        echo ""
        ;;
    *)
        echo "Usage: run-lifecycle-demo.sh [act1|act2|act3|all]" >&2
        exit 1
        ;;
esac
