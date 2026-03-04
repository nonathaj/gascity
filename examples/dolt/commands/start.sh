#!/bin/sh
# gc dolt start — Start the Dolt server if not already running.
#
# Delegates to the gc-beads-bd exec: provider's start operation.
# Used by the dolt-health automation to restart after crashes.
#
# Environment: GC_CITY_PATH
set -e

: "${GC_CITY_PATH:?GC_CITY_PATH must be set}"

script="$GC_CITY_PATH/.gc/bin/gc-beads-bd"

if [ ! -x "$script" ]; then
  echo "gc dolt start: gc-beads-bd not found" >&2
  exit 1
fi

# start exits 0 if started or already running, 2 if remote (no-op).
GC_CITY_PATH="$GC_CITY_PATH" "$script" start
