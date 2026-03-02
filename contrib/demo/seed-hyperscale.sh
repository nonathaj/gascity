#!/usr/bin/env bash
# seed-hyperscale.sh — Seed N work beads for the hyperscale pool.
#
# Usage:
#   ./seed-hyperscale.sh [count] [city-path]
#
# Args:
#   count      — number of beads to create (default: 100)
#   city-path  — city directory (default: ~/hyperscale-demo)

set -euo pipefail

COUNT="${1:-100}"
DEMO_CITY="${2:-${DEMO_CITY:-$HOME/hyperscale-demo}}"

echo "Seeding $COUNT work beads in $DEMO_CITY..."

cd "$DEMO_CITY"

for i in $(seq 1 "$COUNT"); do
    bd create "Task $i of $COUNT" --labels pool:worker 2>/dev/null || true
    if (( i % 10 == 0 )); then
        echo "  Seeded $i/$COUNT beads..."
    fi
done

echo "Done: $COUNT beads seeded (label: pool:worker)"
