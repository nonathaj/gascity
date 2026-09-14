#!/usr/bin/env bash
# backup_dest.sh — resolve a Dolt database's backup destination by URL and
# measure the freshness of the artifact it points at.
#
# WHY THIS EXISTS
#
# mol-dog-backup.sh used to decide whether a database "had a backup" by
# grepping `dolt backup` for one literal name, `<db>-backup`. A database whose
# only destination was named `default` — which is what an external producer
# such as a federation-wide sync daemon configures — failed that grep, so the
# script ran `dolt backup add <db>-backup <url>` against the URL `default`
# already occupied, dolt refused with `address conflict with a remote:
# 'default' -> <url>` (exit 1), the message went to /dev/null, and the run
# reported `<db>(backup add failed)` for a database whose artifact was twenty
# minutes old. Every escalation the order sent for two weeks was that line,
# fourteen times over. mol-dog-doctor.sh carries the same grep.
#
# The functions here replace the name test with the two facts an operator
# actually needs: WHICH configured destination points at the expected URL
# (under any name), and WHEN a backup last completed there. They are shared
# so the backup order and the doctor cannot disagree about either.
#
# CONTRACT
#
# Functions only; sourcing this file has no side effects. It expects
# `run_bounded` (assets/scripts/runtime.sh) to be defined by the caller and
# `dolt` on PATH. Nothing here assumes GNU date or GNU stat: the pack ships a
# Windows mkdir-lock fallback for a reason, and every host-specific call below
# has a BSD and a python3 fallback.
#
# No `2>/dev/null` sits between a tool whose failure is reported and the
# point where its stderr is captured. Tool stderr is kept in a temp file and
# its last non-blank line (first 400 characters — the line bd and dolt put the
# diagnosis on) is left in BACKUP_LAST_ERR for the caller to quote.
#
# backup_list_destinations <db_dir> <bound_secs>
#     Runs `dolt backup -v` in <db_dir> under `run_bounded <bound_secs>` and
#     prints one `name<TAB>url` line per destination on stdout. `-v` is what
#     carries the URL: `dolt backup` alone prints names only. Fields 1 and 2
#     of each line are taken and the rest ignored — the live server prints a
#     third field `{}` and a fresh repo prints a trailing space. Returns 0, or
#     124/137 when the bound fired (coreutils timeout with --kill-after), or
#     the tool's own code. BACKUP_LAST_ERR holds the last stderr line.
#     NOTE: BACKUP_LAST_ERR is set in the calling shell, so call this with
#     stdout redirected to a file (`backup_list_destinations d 30 >"$f"`),
#     not inside a command substitution — `$(...)` runs it in a subshell and
#     the variable is lost with it.
#
# backup_resolve_destination <db> <expected_url> <listing>
#     Prints the name of the first destination whose URL string-equals
#     <expected_url>; else the destination named `<db>-backup` at any URL
#     (the legacy convention, still honored so an operator's cloud backup
#     under that name keeps working); else prints nothing and returns 1.
#     String equality only: dolt does not canonicalize paths either, and a
#     caller that misses here falls into dolt's own `address conflict`
#     message on `backup add`, which names the occupant.
#
# backup_destination_url <name> <listing>
#     Prints the URL listed for <name>, or nothing (returns 1).
#
# backup_manifest_mtime <url>
#     For a file:// URL, prints the mtime (epoch seconds) of <path>/manifest,
#     or nothing (returns 1) when the URL is not file:// or no manifest
#     exists there.
#
# backup_newest_manifest <listing> <expected_url>
#     Over every file:// URL in <listing> plus <expected_url>, prints
#     `epoch<TAB>path` for the newest manifest, or nothing (returns 1) when
#     no manifest exists anywhere. A no-op `dolt backup sync` touches the
#     manifest (measured on dolt 2.2.1), so its mtime is exactly "when a
#     sync last completed here".
#
# backup_fmt_epoch <epoch>      -> ISO-8601 with a numeric zone, e.g.
#                                  2026-09-09T12:24:11-04:00
# backup_fmt_age <secs>         -> 18m / 2h37m / 51h
# backup_last_err <file>        -> the last non-blank line of <file>, cut to
#                                  400 characters; empty when there is none

# Read by callers after backup_list_destinations; set here so `set -u`
# callers can read it before any call.
# shellcheck disable=SC2034
BACKUP_LAST_ERR=""

backup_last_err() { # <file>
    { grep -v '^[[:space:]]*$' "$1" || true; } | tail -n1 | cut -c1-400
}

# backup_file_mtime prints a file's mtime as epoch seconds, or 0. Same form as
# mol-dog-doctor.sh's file_mtime: GNU stat first, then BSD stat.
backup_file_mtime() { # <path>
    local mtime
    mtime=$(stat -c %Y "$1" 2>/dev/null || stat -f %m "$1" 2>/dev/null || echo 0)
    case "$mtime" in
        ''|*[!0-9]*) mtime=0 ;;
    esac
    printf '%s\n' "$mtime"
}

backup_list_destinations() { # <db_dir> <bound_secs>
    local db_dir="$1" bound="$2" err_file out rc=0
    BACKUP_LAST_ERR=""
    if ! err_file="$(mktemp)" || [ -z "$err_file" ]; then
        # shellcheck disable=SC2034
        BACKUP_LAST_ERR="mktemp failed: cannot capture dolt stderr (check \$TMPDIR)"
        return 1
    fi
    out="$( (cd "$db_dir" && run_bounded "$bound" dolt backup -v) 2>"$err_file" )" || rc=$?
    # shellcheck disable=SC2034
    BACKUP_LAST_ERR="$(backup_last_err "$err_file")"
    rm -f "$err_file"
    if [ "$rc" -eq 0 ]; then
        printf '%s\n' "$out" | awk 'NF >= 2 {print $1 "\t" $2}'
    fi
    return "$rc"
}

backup_resolve_destination() { # <db> <expected_url> <listing>
    local db="$1" expected_url="$2" listing="$3" name url legacy=""
    while IFS=$'\t' read -r name url; do
        [ -n "$name" ] || continue
        if [ "$url" = "$expected_url" ]; then
            printf '%s\n' "$name"
            return 0
        fi
        if [ -z "$legacy" ] && [ "$name" = "${db}-backup" ]; then
            legacy="$name"
        fi
    done <<EOF
$listing
EOF
    if [ -n "$legacy" ]; then
        printf '%s\n' "$legacy"
        return 0
    fi
    return 1
}

backup_destination_url() { # <name> <listing>
    local want="$1" listing="$2" name url
    while IFS=$'\t' read -r name url; do
        if [ "$name" = "$want" ]; then
            printf '%s\n' "$url"
            return 0
        fi
    done <<EOF
$listing
EOF
    return 1
}

backup_manifest_mtime() { # <url>
    local url="$1" path
    case "$url" in
        file://*) path="${url#file://}" ;;
        *) return 1 ;;
    esac
    [ -f "$path/manifest" ] || return 1
    backup_file_mtime "$path/manifest"
}

backup_newest_manifest() { # <listing> <expected_url>
    local listing="$1" expected_url="$2" url mtime best_mtime=0 best_path=""
    while IFS= read -r url; do
        [ -n "$url" ] || continue
        mtime="$(backup_manifest_mtime "$url" || true)"
        [ -n "$mtime" ] || continue
        if [ "$mtime" -gt "$best_mtime" ]; then
            best_mtime="$mtime"
            best_path="${url#file://}"
        fi
    done <<EOF
$(printf '%s\n' "$listing" | awk -F'\t' 'NF >= 2 {print $2}')
$expected_url
EOF
    [ -n "$best_path" ] || return 1
    printf '%s\t%s\n' "$best_mtime" "$best_path"
}

backup_fmt_epoch() { # <epoch>
    local epoch="$1" out
    case "$epoch" in
        ''|*[!0-9]*) printf 'unknown\n'; return 1 ;;
    esac
    # GNU date, then BSD date, then python3. %z is portable where %:z is not,
    # so the colon is inserted here.
    if out="$(date -d "@$epoch" +%Y-%m-%dT%H:%M:%S%z 2>/dev/null)" && [ -n "$out" ]; then
        :
    elif out="$(date -r "$epoch" +%Y-%m-%dT%H:%M:%S%z 2>/dev/null)" && [ -n "$out" ]; then
        :
    elif command -v python3 >/dev/null 2>&1; then
        python3 -c 'import datetime, sys
print(datetime.datetime.fromtimestamp(int(sys.argv[1])).astimezone().isoformat(timespec="seconds"))' "$epoch"
        return $?
    else
        printf '@%s\n' "$epoch"
        return 1
    fi
    printf '%s:%s\n' "${out%??}" "${out#"${out%??}"}"
}

backup_fmt_age() { # <secs>
    local secs="$1" hours minutes
    case "$secs" in
        ''|*[!0-9]*) printf '?\n'; return 1 ;;
    esac
    if [ "$secs" -lt 3600 ]; then
        printf '%dm\n' $((secs / 60))
        return 0
    fi
    hours=$((secs / 3600))
    minutes=$(((secs % 3600) / 60))
    if [ "$secs" -ge 86400 ] || [ "$minutes" -eq 0 ]; then
        printf '%dh\n' "$hours"
    else
        printf '%dh%dm\n' "$hours" "$minutes"
    fi
}
