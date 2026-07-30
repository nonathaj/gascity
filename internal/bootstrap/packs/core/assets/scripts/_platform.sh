#!/bin/sh
# _platform.sh — normalize external tool behavior so pack scripts can be written
# once, portably, and behave the same on every host.
#
# Source this from any pack script that shells out to a tool whose behavior
# differs by platform:
#
#     . "$__SCRIPT_DIR/_platform.sh"
#
# It defines shell functions that shadow the affected tools. Being functions,
# they apply to every invocation in the sourcing shell and its subshells —
# command substitutions, pipelines, process substitutions — so call sites stay
# idiomatic. They do NOT reach a separate process that execs the tool directly
# (`xargs jq`, `find -exec jq`); those need the flag spelled out.
#
# Deliberately NOT folded into _bd_trace.sh: that file owns bd invocation
# tracing, and platform normalization is an unrelated concern that will grow.
#
# This copy belongs to the core pack. `assets/` is private to its owning pack
# (pack-spec 0.4), so another pack does not source this file — it carries its own
# copy. The pattern is documented for pack authors in pack-spec 1.3.1.

# jq: force LF output on Windows.
#
# A native Windows jq build opens stdout in text mode, so `\n` becomes `\r\n`.
# Every other tool these scripts use (sed, grep, awk, head, cat, mktemp) comes
# from the sh distribution and already emits LF; jq is the exception because it
# is the one tool Git for Windows does not ship, so it is whatever native build
# the host installed.
#
# The damage is silent and shape-dependent. MSYS pipes translate CRLF to LF, so
# `jq … | while read` looks fine — but a process substitution, here-string or
# file redirect does not, and `read` then leaves a trailing "\r" glued to the
# last field. In orphan-sweep that made an assignee compare unequal to its own
# value read back from `gc bd show`, so beads assigned to dead agents were
# reported "unverifiable" and never reclaimed (gw-14t).
#
# `-b` is used only on Windows. It exists solely for this purpose there, and it
# must never reach a Unix jq: `--binary` is recognized outside jq's
# `#ifdef WIN32` but its `continue` is inside it, so on Unix the argument falls
# through to jq's unknown-option check and aborts. Defining the wrapper only on
# Windows keeps that off Unix entirely rather than relying on how a given jq
# version happens to handle the flag.
#
# Requires jq >= 1.7 on Windows, where `-b` was introduced. Earlier jq builds
# there emit CRLF with no way to suppress it.

# gc_platform_is_windows: true on a Windows host, by whichever check is cheapest.
#
# `OS=Windows_NT` is set by Windows itself and survives into sh, so the common
# case costs no process — worth having because process creation on Windows is
# ~150ms (see engdocs/contributors/windows-portability.md).
#
# But OS is INHERITED, not intrinsic, so any caller that rebuilds a hermetic
# environment drops it — `env -i` wrappers and Go tests that assign cmd.Env from
# a fixed list both do. Relying on OS alone made this shim silently inert under
# exactly those callers, which is how it is most likely to be exercised. `uname`
# costs one process, but only once per script and only when OS is absent.
gc_platform_is_windows() {
    [ "${OS:-}" = "Windows_NT" ] && return 0
    case "$(uname -s 2>/dev/null || true)" in
        MINGW* | MSYS* | CYGWIN* | Windows_NT*) return 0 ;;
    esac
    return 1
}

if gc_platform_is_windows; then
    jq() { command jq -b "$@"; }
fi
