#!/usr/bin/env bash
set -euo pipefail
cd "$GC_PACK_DIR/server"

API_FLAG=()
if [ -n "${GC_API_URL:-}" ]; then
    API_FLAG=(-api "$GC_API_URL")
fi

exec go run . -city "$GC_CITY_PATH" -city-name "$GC_CITY_NAME" "${API_FLAG[@]}" "$@"
