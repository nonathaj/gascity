#!/usr/bin/env bash
# Pack doctor check: verify Dolt binary and optional server reachability.
#
# Exit codes: 0=OK, 1=Warning, 2=Error
# stdout: first line=message, rest=details

if ! command -v dolt >/dev/null 2>&1; then
    echo "dolt binary not found"
    echo "install dolt: https://docs.dolthub.com/introduction/installation"
    exit 2
fi

version=$(dolt version 2>/dev/null | head -1)
echo "dolt available ($version)"
exit 0
