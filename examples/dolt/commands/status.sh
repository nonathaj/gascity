#!/bin/sh
# gc dolt status — Check if the Dolt server is running.
#
# Exits 0 if the server is reachable, 1 otherwise.
# Used by the dolt-health automation to detect crashes.
#
# Environment: GC_CITY_PATH
set -e

: "${GC_CITY_PATH:?GC_CITY_PATH must be set}"

script="$GC_CITY_PATH/.gc/bin/gc-beads-bd"

if [ ! -x "$script" ]; then
  echo "gc dolt status: gc-beads-bd not found" >&2
  exit 1
fi

# probe exits 0 if running, 2 if not running.
GC_CITY_PATH="$GC_CITY_PATH" "$script" probe >/dev/null 2>&1
