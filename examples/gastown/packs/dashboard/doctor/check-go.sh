#!/usr/bin/env bash
# Check that Go is installed (required to run the dashboard server).
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
    echo "FAIL: go not found in PATH"
    echo "  Install Go from https://go.dev/dl/"
    exit 1
fi

version=$(go version | awk '{print $3}')
echo "OK: $version"
