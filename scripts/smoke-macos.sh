#!/bin/bash
# smoke-macos.sh — verify a released gc binary works on macOS.
# Run before tagging a release to catch packaging/platform regressions.
#
# The gc binary runs inside a macOS sandbox-exec jail:
#   - Filesystem writes restricted to a temp directory
#   - Network access denied
#   - All artifacts cleaned up on exit
#
# The download happens BEFORE the sandbox is applied.
#
# Usage:
#   ./scripts/smoke-macos.sh                     # latest release, arm64
#   GC_VERSION=v0.13.4 ./scripts/smoke-macos.sh  # specific version
#   GC_ARCH=amd64 ./scripts/smoke-macos.sh       # Intel binary

set -euo pipefail

ARCH="${GC_ARCH:-arm64}"
VERSION="${GC_VERSION:-latest}"
REPO="gastownhall/gascity"

# --- Platform gate ---
if [[ "$(uname)" != "Darwin" ]]; then
    echo "ERROR: this script must run on macOS" >&2
    exit 1
fi

# --- Sandbox ---
SANDBOX=$(mktemp -d -t gc-smoke-XXXXXX)
# macOS symlinks /var -> /private/var; sandbox-exec needs both paths.
SANDBOX_REAL=$(cd "$SANDBOX" && pwd -P)

export HOME="$SANDBOX/home"
export XDG_CONFIG_HOME="$SANDBOX/config"
export XDG_DATA_HOME="$SANDBOX/data"
export XDG_CACHE_HOME="$SANDBOX/cache"
mkdir -p "$HOME" "$XDG_CONFIG_HOME" "$XDG_DATA_HOME" "$XDG_CACHE_HOME"

GC="$SANDBOX/gc"
PASS=0
FAIL=0
SKIP=0

cleanup() {
    rm -rf "$SANDBOX"
}
trap cleanup EXIT

result() {
    local status=$1 name=$2
    case "$status" in
        pass) echo "  PASS  $name"; PASS=$((PASS + 1)) ;;
        fail) echo "  FAIL  $name"; FAIL=$((FAIL + 1)) ;;
        skip) echo "  SKIP  $name"; SKIP=$((SKIP + 1)) ;;
    esac
}

# --- Generate sandbox-exec profile ---
SBPROFILE="$SANDBOX/gc-smoke.sb"
cat > "$SBPROFILE" <<SBEOF
(version 1)
(deny default)

;; Read access to the filesystem (binaries, dylibs, frameworks, etc.)
(allow file-read*)

;; Write access only inside the sandbox temp dir (both symlink and real path)
(allow file-write* (subpath "$SANDBOX"))
(allow file-write* (subpath "$SANDBOX_REAL"))
(allow file-write* (subpath "/dev"))

;; Process execution (gc may fork for doctor checks, init, etc.)
(allow process-exec)
(allow process-fork)

;; Go runtime needs sysctl and mach ports
(allow sysctl-read)
(allow mach-lookup)

;; No network access — the binary should not phone home
(deny network*)
SBEOF

gc_sandboxed() {
    sandbox-exec -f "$SBPROFILE" "$GC" "$@"
}

echo "=== Gas City macOS Smoke Test ==="
echo "Sandbox:      $SANDBOX"
echo "Arch:         $ARCH"
echo "Containment:  sandbox-exec (no network, writes restricted)"
echo ""

# --- Download (outside sandbox — needs network) ---
echo "--- Download gc binary ---"
if [[ "$VERSION" == "latest" ]]; then
    TAG=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"tag_name"' | head -1 | cut -d'"' -f4)
    if [[ -z "$TAG" ]]; then
        echo "ERROR: could not resolve latest release tag" >&2
        exit 1
    fi
else
    TAG="$VERSION"
fi

NUMERIC="${TAG#v}"
ARCHIVE="gascity_${NUMERIC}_darwin_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$TAG/$ARCHIVE"

echo "Release: $TAG"
echo "URL:     $URL"

if ! curl -fsSL "$URL" -o "$SANDBOX/$ARCHIVE"; then
    echo "ERROR: download failed" >&2
    exit 1
fi

tar -xzf "$SANDBOX/$ARCHIVE" -C "$SANDBOX"
if [[ ! -x "$GC" ]]; then
    echo "ERROR: gc binary not found after extraction" >&2
    exit 1
fi

# Strip macOS quarantine attribute so Gatekeeper doesn't block execution.
xattr -d com.apple.quarantine "$GC" 2>/dev/null || true
result pass "download + extract"

# --- All tests below run gc inside the sandbox-exec jail ---

# --- Test: version ---
echo ""
echo "--- Test: gc version ---"
VERSION_OUT=$(gc_sandboxed version 2>&1) || true
if [[ -n "$VERSION_OUT" ]]; then
    echo "  $VERSION_OUT"
    result pass "version"
else
    result fail "version"
fi

# --- Test: version --long ---
echo ""
echo "--- Test: gc version --long ---"
if gc_sandboxed version --long >/dev/null 2>&1; then
    result pass "version --long"
else
    result skip "version --long (flag not supported)"
fi

# --- Test: help ---
echo ""
echo "--- Test: gc help ---"
if gc_sandboxed help >/dev/null 2>&1; then
    result pass "help"
else
    result fail "help"
fi

# --- Test: doctor ---
echo ""
echo "--- Test: gc doctor ---"
if gc_sandboxed doctor --help >/dev/null 2>&1; then
    # doctor will report missing deps on a clean machine — that's expected.
    gc_sandboxed doctor 2>&1 | head -30 || true
    result pass "doctor (runs; dependency warnings expected)"
else
    result skip "doctor (not available)"
fi

# --- Test: init ---
echo ""
echo "--- Test: gc init ---"
INIT_DIR="$SANDBOX/test-city"
# gc init is interactive; pipe empty stdin so it falls back to defaults.
# init may exit non-zero if optional deps (bd, flock) are missing — that's OK
# as long as it scaffolds the city directory.
gc_sandboxed init "$INIT_DIR" </dev/null 2>&1 | tail -5 || true
if [[ -d "$INIT_DIR" ]]; then
    result pass "init (created city dir)"
else
    result fail "init (no directory created)"
fi

# --- Summary ---
echo ""
echo "==========================================="
echo "  Results: $PASS passed, $FAIL failed, $SKIP skipped"
echo "  Binary:  gc-darwin-$ARCH ($TAG)"
echo "==========================================="

if [[ $FAIL -gt 0 ]]; then
    exit 1
fi
