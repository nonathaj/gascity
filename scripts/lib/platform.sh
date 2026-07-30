#!/usr/bin/env sh
# Platform prelude for repo scripts. Source it before invoking tools whose
# native-Windows builds behave differently from their Unix builds:
#
#     . "$(dirname "$0")/lib/platform.sh"
#
# This is the repo-tooling twin of the pack-facing prelude at
# internal/bootstrap/packs/core/assets/scripts/_platform.sh. The duplication is
# deliberate: that file ships to users inside a pack and must be resolvable
# relative to a pack's own asset directory, while this one is dev tooling that
# never leaves the repo. Keep the two in sync when adding a shim.

gc_platform_is_windows() {
    [ "${OS:-}" = "Windows_NT" ] && return 0
    case "$(uname -s 2>/dev/null || true)" in
        MINGW* | MSYS* | CYGWIN* | Windows_NT*) return 0 ;;
    esac
    return 1
}

if gc_platform_is_windows; then
    # jq's native Windows build opens stdout in text mode and terminates every
    # line with CRLF. An MSYS pipe strips the CR again, so the corruption is
    # invisible when one script pipes jq into another -- but it survives into a
    # file, a process substitution, or a pipe read by a native Windows process
    # such as `go test` capturing a script's output. `jq -b` (jq 1.7+) forces
    # binary output and LF endings, matching Unix byte for byte.
    jq() { command jq -b "$@"; }
fi
