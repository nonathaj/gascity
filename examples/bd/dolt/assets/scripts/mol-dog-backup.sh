#!/usr/bin/env bash
# mol-dog-backup — report per-database Dolt backup artifact freshness and, in
# sync mode, sync each database to its backup destination and rsync the
# artifacts offsite.
#
# Runs as an exec order (no LLM, no agent, no wisp). All operations are
# deterministic.
#
# WHAT A VERDICT MEANS
#
# Every verdict this script prints is a fact about the backup artifact on
# disk — the `manifest` a `dolt backup sync` writes at a file:// destination —
# never about this script's own plumbing:
#
#   fresh            the newest manifest at any of the database's file://
#                    destinations is younger than GC_BACKUP_STALE_SECS
#   stale            older than that; the entry says whether the content is
#                    complete as of the last commit (nothing to sync), behind
#                    (commits newer than the artifact), or not checked
#   missing          no manifest exists at any destination — never backed up
#   could not check  the destination list could not be read (timeout or tool
#                    failure) or the database directory was not found; the
#                    artifact age at the expected path is printed as detail
#                    and does NOT make the database fresh
#   synced           (sync mode) dolt reported a successful sync this run
#   sync failed / sync timeout / add failed
#                    (sync mode) the tool's own words and exit code, beside
#                    the artifact's age or `missing`
#
# "I could not do the thing I wanted to do" is reported as could-not-check
# or as the tool's message. It is never rendered as "the backup failed" — the
# script used to turn `dolt backup add` refusing a second name at an already
# occupied URL into `<db>(backup add failed)` for every database whose only
# destination was named `default`, and escalated that as a fleet-wide failure
# on every run while the artifacts landed hourly. A destination is resolved by
# URL now, under any name (assets/scripts/backup_dest.sh).
#
# TWO MODES — GC_BACKUP_MODE=sync|verify
#
#   sync    (default) This order is the producer. Every database with a
#           destination at file://$BACKUP_ARTIFACT_DIR/<db> (any name) or a
#           legacy `<db>-backup` destination is synced to it; a database with
#           neither gets `<db>-backup` auto-configured under the artifact dir
#           first; then the artifact dir is rsynced to GC_BACKUP_OFFSITE_PATH
#           when set.
#   verify  ANOTHER PROCESS IS THE PRODUCER. This script never runs
#           `dolt backup add`, `dolt backup sync` or `dolt backup sync-url`.
#           It lists destinations, measures artifact freshness, reads the
#           HEAD commit time of a clock-stale database to say whether its
#           content is complete, and escalates findings. Use this wherever a
#           separate writer already refreshes the artifact directories and a
#           second, unserialized writer must not race it into the same
#           directory: in the federation that writer is fed_sync (systemd
#           user timer fed-sync.timer, hourly), which writes .dolt-backup/<db>
#           for every live store. A no-op sync would only refresh a manifest
#           and hide that nothing else is syncing that store.
#
#   Any other value: exit 2 with a message naming the knob, nothing synced. A
#   misconfigured sentinel must not silently become a writer.
#
# BOUNDS
#
# Measured before choosing them: the remote list (`dolt backup -v`) returned
# in 0.15-0.18 s at load 12.99 on the host where the false escalations were
# reported (that host ran 24-34 on 24 cores with the dolt server near a full
# core while the bug was filed; at measurement the server averaged ~72% of a
# core over 11h49m). 30 s is therefore >150x headroom, and a run that still
# exceeds it is reported as `timeout` (could not check), never as a failed
# backup. THESE BOUNDS ARE NOT THE FIX for the escalations this rewrite
# addresses: those were deterministic (`dolt backup add` refusing a second
# name at an occupied URL), independent of load, and reported identically on
# every run for two weeks. Per database, sync mode spends at most
# LIST + ADD + SYNC = 30 + 30 + 120 s, the budget orders/mol-dog-backup.toml
# is sized for; verify mode at most LIST + CONTENT = 30 + 10 s.
#
# KNOBS
#
#   GC_BACKUP_MODE                  sync | verify (default sync)
#   GC_BACKUP_STALE_SECS            age past which an artifact is stale
#                                   (default 43200 = 12h, the doctor's
#                                   BACKUP_STALE_S, so the two agree)
#   GC_BACKUP_STALE_HINT            text appended to every stale entry; a
#                                   city names its owner here
#   GC_BACKUP_LIST_TIMEOUT_SECS     bound on `dolt backup -v`       (30)
#   GC_BACKUP_ADD_TIMEOUT_SECS      bound on `dolt backup add`      (30)
#   GC_BACKUP_SYNC_TIMEOUT_SECS     bound on `dolt backup sync`     (120)
#   GC_BACKUP_CONTENT_TIMEOUT_SECS  bound on the HEAD commit read   (10)
#   GC_BACKUP_DATABASES             comma-separated allowlist; default is
#                                   every user database in the data dir
#   GC_BACKUP_ARTIFACT_DIR          expected destination root
#                                   (default $GC_CITY_PATH/.dolt-backup)
#   GC_BACKUP_OFFSITE_PATH          rsync target for the artifact dir
#                                   (sync mode; unset = skipped)
#
# Exit codes: 0 whether or not there are findings (the escalation is the
# signal; a non-zero exec exit would make the controller raise a second,
# generic alarm), 1 when dolt is below the backup floor, 2 for could-not-look —
# an invalid GC_BACKUP_MODE, a database-discovery query that failed, or a
# server that named databases the data dir does not hold. Findings are what
# this script went looking for and 0 is deliberate for them; could-not-look
# means it never got to look at all, and the two must not share an exit code.
set -euo pipefail

# `CDPATH= cd` is deliberate: it clears CDPATH for that one cd so a user's
# CDPATH cannot redirect the pack-dir resolution. Not an empty assignment.
# shellcheck disable=SC1007
PACK_DIR="${GC_PACK_DIR:-$(CDPATH= cd -- "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
. "$PACK_DIR/assets/scripts/runtime.sh"
. "$PACK_DIR/assets/scripts/_notify.sh"
. "$PACK_DIR/assets/scripts/backup_dest.sh"

PORT="$GC_DOLT_PORT"
HOST="${GC_DOLT_HOST:-127.0.0.1}"
USER="${GC_DOLT_USER:-root}"
OFFSITE_PATH="${GC_BACKUP_OFFSITE_PATH:-}"
BACKUP_ARTIFACT_DIR="${GC_BACKUP_ARTIFACT_DIR:-$GC_CITY_PATH/.dolt-backup}"
SYSTEM_DBS="^(information_schema|mysql|dolt_cluster|__gc_probe|performance_schema|sys)$"
MIN_DOLT_BACKUP_VERSION="2.1.0"
BACKUP_LOCK_FILE="${GC_DOLT_BACKUP_LOCK_FILE:-$GC_CITY_PATH/.gc/runtime/packs/dolt/backup-sync.lock}"
BACKUP_LOCK_WAIT_SECONDS="${GC_DOLT_BACKUP_LOCK_WAIT_SECONDS:-5}"

# Bounds (see BOUNDS above). Each is validated the way OFFSITE_TIMEOUT is
# validated upstream: empty, non-numeric or 0 falls back to the default,
# because GNU `timeout 0` disables the bound and the python3 fallback
# expires at once.
LIST_TIMEOUT="${GC_BACKUP_LIST_TIMEOUT_SECS:-30}"
ADD_TIMEOUT="${GC_BACKUP_ADD_TIMEOUT_SECS:-30}"
SYNC_TIMEOUT="${GC_BACKUP_SYNC_TIMEOUT_SECS:-120}"
CONTENT_TIMEOUT="${GC_BACKUP_CONTENT_TIMEOUT_SECS:-10}"
case "$LIST_TIMEOUT" in
    ''|*[!0-9]*|0) LIST_TIMEOUT=30 ;;
esac
case "$ADD_TIMEOUT" in
    ''|*[!0-9]*|0) ADD_TIMEOUT=30 ;;
esac
case "$SYNC_TIMEOUT" in
    ''|*[!0-9]*|0) SYNC_TIMEOUT=120 ;;
esac
case "$CONTENT_TIMEOUT" in
    ''|*[!0-9]*|0) CONTENT_TIMEOUT=10 ;;
esac

GC_BACKUP_MODE="${GC_BACKUP_MODE:-sync}"
case "$GC_BACKUP_MODE" in
    sync|verify) ;;
    *)
        echo "backup: invalid GC_BACKUP_MODE=$GC_BACKUP_MODE (want sync or verify); nothing synced" >&2
        exit 2
        ;;
esac
STALE_SECS="${GC_BACKUP_STALE_SECS:-43200}"
case "$STALE_SECS" in
    ''|*[!0-9]*) STALE_SECS=43200 ;;
esac
STALE_HINT="${GC_BACKUP_STALE_HINT:-decide whether this database still needs a producer}"

dolt_sql() {
    DOLT_CLI_PASSWORD="${GC_DOLT_PASSWORD:-}" \
        run_bounded 30 \
        dolt --host "$HOST" --port "$PORT" --user "$USER" --no-tls sql "$@"
}

# dolt_sql_bounded is dolt_sql with the bound as its first argument.
# run_bounded execs a program and cannot bound a shell function, so
# `run_bounded N dolt_sql ...` exits 127; this exists so the content read
# below can carry its own bound.
dolt_sql_bounded() {
    sql_bound="$1"
    shift
    DOLT_CLI_PASSWORD="${GC_DOLT_PASSWORD:-}" \
        run_bounded "$sql_bound" \
        dolt --host "$HOST" --port "$PORT" --user "$USER" --no-tls sql "$@"
}

# last_err prints the last non-blank line of a captured stderr file, cut to
# 400 characters — the line dolt and rsync put the diagnosis on.
last_err() {
    backup_last_err "$1"
}

# new_capture_file prints a fresh temp file for capturing a tool's stderr.
# A mktemp failure is reported, not ignored: without the file the tool's
# words would be lost, and a capture that silently fell back to /dev/null
# would recreate the defect this script was rewritten to remove.
new_capture_file() {
    capture_file=""
    if ! capture_file="$(mktemp)" || [ -z "$capture_file" ]; then
        echo "backup: mktemp failed: cannot capture tool stderr (check \$TMPDIR)" >&2
        return 1
    fi
    printf '%s\n' "$capture_file"
}

dolt_version_at_least() {
    current="${1#v}"
    minimum="$2"
    current="${current%%+*}"
    minimum="${minimum%%+*}"
    case "$current" in
        *-*) return 1 ;;
    esac
    IFS=. read -r cur_major cur_minor cur_patch <<EOF
$current
EOF
    IFS=. read -r min_major min_minor min_patch <<EOF
$minimum
EOF
    for part in "$cur_major" "$cur_minor" "$cur_patch" "$min_major" "$min_minor" "$min_patch"; do
        case "$part" in
            ''|*[!0-9]*) return 1 ;;
        esac
    done
    cur_major=$((10#$cur_major))
    cur_minor=$((10#$cur_minor))
    cur_patch=$((10#$cur_patch))
    min_major=$((10#$min_major))
    min_minor=$((10#$min_minor))
    min_patch=$((10#$min_patch))
    if [ "$cur_major" -ne "$min_major" ]; then
        [ "$cur_major" -gt "$min_major" ]
        return $?
    fi
    if [ "$cur_minor" -ne "$min_minor" ]; then
        [ "$cur_minor" -gt "$min_minor" ]
        return $?
    fi
    [ "$cur_patch" -ge "$min_patch" ]
}

# send_escalation delivers an escalation and reports a delivery failure on
# stderr instead of discarding it. An alert nobody received is not a
# reported failure; the caller appends `escalation: FAILED` to the summary.
send_escalation() {
    local subject="$1"
    local message="$2"
    local err
    if ! err=$(dolt_escalate "$subject" "$message" 2>&1 >/dev/null); then
        echo "backup: escalation failed: ${err:-<no stderr>}" >&2
        return 1
    fi
}

# backup_lock_holder_alive reports whether the pid recorded in the lock
# directory is still running, so a lock orphaned by a crash does not disable
# backups permanently. A lock directory with no readable pid is treated as
# alive: without evidence the holder is gone, the safe answer is to wait.
backup_lock_holder_alive() {
    lock_holder_pid=$(cat "$BACKUP_LOCK_DIR/pid" 2>/dev/null || true)
    case "$lock_holder_pid" in
        ''|*[!0-9]*) return 0 ;;
    esac
    kill -0 "$lock_holder_pid" 2>/dev/null
}

# acquire_backup_lock serializes backup sync across concurrent invocations.
#
# Concurrent `dolt backup sync` against the shared sql-server can overload it,
# so this must be a real mutual exclusion, not best-effort.
#
# flock(1) is used when present because the kernel releases it on process death,
# which no userspace scheme can match. Git for Windows ships no flock, and this
# used to fail closed there -- escalating and exiting 1 -- which meant managed
# backup simply did not run on Windows. That is stronger than the guarantee
# needs: mkdir is atomic on every POSIX filesystem, so a lock DIRECTORY provides
# the same mutual exclusion, and recording the holder's pid lets a lock orphaned
# by a crash be reclaimed rather than blocking backups forever.
#
# This mirrors the fallback commands/compact/run.sh already uses for the same
# reason; see gw-oo5 for unifying the two.
acquire_backup_lock() {
    case "$BACKUP_LOCK_WAIT_SECONDS" in
        ''|*[!0-9]*) BACKUP_LOCK_WAIT_SECONDS=5 ;;
    esac

    mkdir -p "$(dirname "$BACKUP_LOCK_FILE")"

    if command -v flock >/dev/null 2>&1; then
        exec 9>"$BACKUP_LOCK_FILE"
        if ! flock -w "$BACKUP_LOCK_WAIT_SECONDS" 9; then
            SUMMARY="backup — skipped: already running"
            dolt_notify_done "$SUMMARY"
            echo "backup: $SUMMARY"
            exit 0
        fi
        return 0
    fi

    BACKUP_LOCK_DIR="$BACKUP_LOCK_FILE.d"
    backup_lock_waited=0
    while : ; do
        if mkdir "$BACKUP_LOCK_DIR" 2>/dev/null; then
            printf '%s\n' "$$" > "$BACKUP_LOCK_DIR/pid" 2>/dev/null || true
            # Release on every exit path, including the error paths below that
            # exit non-zero. Without this a failed backup would leave the lock
            # behind and the next run would wait out the full timeout.
            trap 'rm -rf "$BACKUP_LOCK_DIR"' EXIT INT TERM
            return 0
        fi
        if ! backup_lock_holder_alive; then
            rm -rf "$BACKUP_LOCK_DIR"
            continue
        fi
        if [ "$backup_lock_waited" -ge "$BACKUP_LOCK_WAIT_SECONDS" ]; then
            break
        fi
        sleep 1
        backup_lock_waited=$((backup_lock_waited + 1))
    done

    SUMMARY="backup — skipped: already running"
    dolt_notify_done "$SUMMARY"
    echo "backup: $SUMMARY"
    exit 0
}

# --- Step 1: Preflight Dolt version before backup sync ---

DOLT_VERSION="$(dolt version 2>/dev/null | awk 'NR == 1 {print $NF}' || true)"
if ! dolt_version_at_least "$DOLT_VERSION" "$MIN_DOLT_BACKUP_VERSION"; then
    send_escalation \
        "Dolt backup: dolt-too-old for backup sync [HIGH]" \
        "Skipping backup sync: dolt version ${DOLT_VERSION:-unknown} is below required ${MIN_DOLT_BACKUP_VERSION}. Gas City requires this managed Dolt floor before backup sync." \
        || true
    SUMMARY="backup — dolt-too-old: ${DOLT_VERSION:-unknown}, required: $MIN_DOLT_BACKUP_VERSION"
    dolt_notify_done "$SUMMARY"
    echo "backup: $SUMMARY"
    exit 1
fi

acquire_backup_lock

# tool_failure renders "<what>: <last stderr line> (exit N)", or the timeout
# form "<what> timeout (Ns)[: <stderr>]" for 124/137. The stderr is quoted on
# the timeout form too, so a run_bounded that returned 124 because no
# bounding mechanism exists is distinguishable from a real timeout.
tool_failure() {
    local what="$1" rc="$2" err="$3" bound="$4"
    case "$rc" in
        124|137)
            if [ -n "$err" ]; then
                printf '%s timeout (%ss): %s' "$what" "$bound" "$err"
            else
                printf '%s timeout (%ss)' "$what" "$bound"
            fi
            ;;
        *)
            printf '%s: %s (exit %s)' "$what" "${err:-<no stderr>}" "$rc"
            ;;
    esac
}

# --- Step 2: Discover databases ---

# If GC_BACKUP_DATABASES is set, use it; otherwise auto-discover every user
# database in the data dir. Discovery used to require an existing <db>-backup
# remote, silently excluding unconfigured DBs from backup coverage — which is
# how production DBs ended up unrecoverable after journal corruption (#3176:
# beads_hq had no named remote, so it was never synced). DBs without any
# destination get one auto-configured below, in sync mode.
#
# Auto-discovery has three outcomes and they used to render identically. The
# query's stderr went to /dev/null and `|| true` discarded its exit status, so
# the only thing consulted afterwards was whether the list came back empty: a
# refused socket printed `no databases found, skipping` and exited 0, byte for
# byte what a healthy server with nothing to back up prints. This order IS the
# federation's backup sentinel, and the outage that takes the Dolt server down
# is the same one that stops fed_sync writing artifacts — so the watchman went
# silent at exactly the moment the thing it watches became unreachable, every
# six hours, indefinitely. The three are separated below: a query that failed
# is could-not-look, a server naming databases the data dir does not hold is
# could-not-look, and only a genuinely empty fleet is the benign skip.
if [ -n "${GC_BACKUP_DATABASES:-}" ]; then
    DATABASES=$(echo "$GC_BACKUP_DATABASES" | tr ',' '\n' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^$' || true)
else
    if err_file="$(new_capture_file)"; then
        rc=0
        ALL_DBS="$(dolt_sql -r csv -q "SHOW DATABASES" 2>"$err_file")" || rc=$?
        db_err="$(last_err "$err_file")"
        rm -f "$err_file"
    else
        rc=1
        db_err="mktemp failed, cannot capture dolt stderr (check \$TMPDIR)"
    fi
    if [ "$rc" -ne 0 ]; then
        # 30 is dolt_sql's own run_bounded bound, so a 124 renders against the
        # bound that actually fired rather than against a number invented here.
        send_escalation \
            "Dolt backup: could not enumerate databases [HIGH]" \
            "$(tool_failure "SHOW DATABASES" "$rc" "$db_err" 30) — no database was checked this run." \
            || true
        SUMMARY="backup — could not check: database discovery failed"
        dolt_notify_done "$SUMMARY"
        echo "backup: $SUMMARY" >&2
        exit 2
    fi
    ALL_DBS="$(printf '%s\n' "$ALL_DBS" | tail -n +2 | grep -viE "$SYSTEM_DBS" || true)"
    DATABASES=""
    for db in $ALL_DBS; do
        if [ -d "$DOLT_DATA_DIR/$db/.dolt" ]; then
            DATABASES="$DATABASES $db"
        fi
    done
    DATABASES=$(echo "$DATABASES" | tr ' ' '\n' | grep -v '^$' || true)
    if [ -z "$DATABASES" ] && [ -n "$ALL_DBS" ]; then
        send_escalation \
            "Dolt backup: no listed database is present in the data dir [HIGH]" \
            "dolt listed: $(printf '%s' "$ALL_DBS" | tr '\n' ' ') — none has a .dolt directory under $DOLT_DATA_DIR. No database was checked this run." \
            || true
        SUMMARY="backup — could not check: no listed database present under $DOLT_DATA_DIR"
        dolt_notify_done "$SUMMARY"
        echo "backup: $SUMMARY" >&2
        exit 2
    fi
fi

# The genuinely empty fleet: the server answered, named nothing this script is
# responsible for, and that is the whole truth. It gets the completion signal
# every other terminal path in this script sends — this was the only one that
# skipped it.
if [ -z "$DATABASES" ]; then
    SUMMARY="backup — no user databases found"
    dolt_notify_done "$SUMMARY"
    echo "backup: no databases found, skipping"
    exit 0
fi

# --- Step 3: Per-database verdicts ---

TOTAL=$(printf '%s\n' "$DATABASES" | awk 'NF {count++} END {print count + 0}')
SYNCED=0     # sync mode: dolt reported success
VERIFIED=0   # verify mode: artifact fresh
STALE=0
MISSING=0
UNCHECKED=0  # could not check: list timeout/failure, not found
FAILED=0     # sync mode: add failed / sync failed / sync timeout with an existing artifact
FINDINGS=""  # the finding entries, one per line, verbatim from the run output
HIGH_RISK=0  # any missing / behind / content-not-checked database

append_finding() {
    if [ -n "$FINDINGS" ]; then
        FINDINGS="$FINDINGS
$1"
    else
        FINDINGS="$1"
    fi
}

# artifact_age_clause prints "(<age>)" or "(no manifest)" for one file:// URL.
artifact_age_clause() {
    local url="$1" now="$2" mtime age
    mtime="$(backup_manifest_mtime "$url" || true)"
    if [ -z "$mtime" ]; then
        printf '(no manifest)'
        return 0
    fi
    age=$((now - mtime))
    [ "$age" -ge 0 ] || age=0
    printf '(%s)' "$(backup_fmt_age "$age")"
}

for db in $DATABASES; do
    db_dir="$DOLT_DATA_DIR/$db"
    expected_url="file://$BACKUP_ARTIFACT_DIR/$db"
    listing=""
    list_state="ok"      # ok | timeout | failed
    list_detail=""
    dest=""
    dest_url=""
    dest_note=""         # e.g. auto-configured, present under another name
    add_detail=""        # add failed: ...
    sync_state=""        # synced | failed | timeout
    sync_detail=""
    verdict=""
    reason=""
    finding=0
    high=0

    # 1. The database directory must exist for `dolt backup` to run in it.
    if [ ! -d "$db_dir/.dolt" ]; then
        verdict="not found"
        list_state="failed"
        list_detail="database directory $db_dir/.dolt does not exist (could not check)"
    else
        # 2. List the configured destinations, by URL. stdout goes to a file
        #    so BACKUP_LAST_ERR reaches this shell.
        rc=0
        if list_file="$(new_capture_file)"; then
            backup_list_destinations "$db_dir" "$LIST_TIMEOUT" >"$list_file" || rc=$?
            listing="$(cat "$list_file")"
            rm -f "$list_file"
            case "$rc" in
                0) list_state="ok" ;;
                124|137)
                    list_state="timeout"
                    list_detail="$(tool_failure "destination list" "$rc" "$BACKUP_LAST_ERR" "$LIST_TIMEOUT")"
                    ;;
                *)
                    list_state="failed"
                    list_detail="$(tool_failure "destination list failed" "$rc" "$BACKUP_LAST_ERR" "$LIST_TIMEOUT")"
                    ;;
            esac
        else
            list_state="failed"
            list_detail="destination list failed: mktemp failed, cannot capture dolt stderr (check \$TMPDIR)"
        fi

        # 3. Resolve the destination: the one at the expected URL under any
        #    name, else the legacy <db>-backup name at any URL.
        dest="$(backup_resolve_destination "$db" "$expected_url" "$listing" || true)"
        if [ -n "$dest" ]; then
            dest_url="$(backup_destination_url "$dest" "$listing" || true)"
        fi

        # 4. Sync mode only: add a missing destination, then sync. Verify
        #    mode never runs backup add, backup sync or backup sync-url.
        if [ "$GC_BACKUP_MODE" = "sync" ]; then
            if [ -z "$dest" ] && [ "$list_state" = "ok" ]; then
                mkdir -p "$BACKUP_ARTIFACT_DIR/$db"
                rc=0
                if err_file="$(new_capture_file)"; then
                    (cd "$db_dir" && run_bounded "$ADD_TIMEOUT" dolt backup add "${db}-backup" "$expected_url") >/dev/null 2>"$err_file" || rc=$?
                    add_err="$(last_err "$err_file")"
                    rm -f "$err_file"
                else
                    rc=1
                    add_err="mktemp failed, cannot capture dolt stderr (check \$TMPDIR)"
                fi
                if [ "$rc" -eq 0 ]; then
                    echo "backup: auto-configured missing backup remote ${db}-backup -> $expected_url"
                    dest="${db}-backup"
                    dest_url="$expected_url"
                    dest_note="auto-configured"
                else
                    case "$add_err" in
                        *"address conflict with a remote: '"*)
                            # dolt names the destination already at that URL:
                            # `address conflict with a remote: '<name>' -> <url>`.
                            dest="${add_err#*address conflict with a remote: \'}"
                            dest="${dest%%\'*}"
                            dest_url="$expected_url"
                            dest_note="present under another name, per dolt: $add_err"
                            ;;
                        *"already exists"*)
                            dest="${db}-backup"
                            dest_note="present, per dolt: $add_err"
                            ;;
                        *)
                            add_detail="$(tool_failure "backup add ${db}-backup $expected_url" "$rc" "$add_err" "$ADD_TIMEOUT")"
                            ;;
                    esac
                fi
            fi

            sync_what=""
            if [ -n "$dest" ]; then
                sync_what="backup sync $dest"
            elif [ "$list_state" = "timeout" ]; then
                # The list timed out, so no name is known; sync-url needs
                # none. The sync is still attempted (REQ-001).
                sync_what="backup sync-url $expected_url"
            fi
            if [ -n "$sync_what" ]; then
                rc=0
                if err_file="$(new_capture_file)"; then
                    if [ -n "$dest" ]; then
                        (cd "$db_dir" && run_bounded "$SYNC_TIMEOUT" dolt backup sync "$dest") >/dev/null 2>"$err_file" || rc=$?
                    else
                        (cd "$db_dir" && run_bounded "$SYNC_TIMEOUT" dolt backup sync-url "$expected_url") >/dev/null 2>"$err_file" || rc=$?
                    fi
                    sync_err="$(last_err "$err_file")"
                    rm -f "$err_file"
                else
                    rc=1
                    sync_err="mktemp failed, cannot capture dolt stderr (check \$TMPDIR)"
                fi
                case "$rc" in
                    0)
                        sync_state="synced"
                        sync_detail="synced: $sync_what ok"
                        ;;
                    124|137)
                        sync_state="timeout"
                        sync_detail="sync timeout: $(tool_failure "$sync_what" "$rc" "$sync_err" "$SYNC_TIMEOUT")"
                        ;;
                    *)
                        sync_state="failed"
                        sync_detail="sync failed: $(tool_failure "$sync_what" "$rc" "$sync_err" "$SYNC_TIMEOUT")"
                        ;;
                esac
            fi
        fi
    fi

    # 5. Freshness, both modes: the newest manifest across every file://
    #    destination plus the expected path.
    now="$(date +%s)"
    artifact_epoch=""
    artifact_path=""
    artifact_age=""
    newest="$(backup_newest_manifest "$listing" "$expected_url" || true)"
    if [ -n "$newest" ]; then
        artifact_epoch="${newest%%$'\t'*}"
        artifact_path="${newest#*$'\t'}"
        artifact_age=$((now - artifact_epoch))
        [ "$artifact_age" -ge 0 ] || artifact_age=0
        artifact_clause="last artifact $(backup_fmt_epoch "$artifact_epoch") ($(backup_fmt_age "$artifact_age"))"
    else
        artifact_clause="last artifact: none found"
    fi

    # 6. Verdict.
    if [ "$verdict" = "not found" ]; then
        [ -z "$artifact_epoch" ] || artifact_clause="$artifact_clause — expected path only, freshness not verified"
        UNCHECKED=$((UNCHECKED + 1))
        finding=1
    elif [ "$GC_BACKUP_MODE" = "sync" ]; then
        if [ "$list_state" = "timeout" ]; then
            # The check did not complete: could not check, whatever the
            # sync-url attempt did. Its result is in the entry.
            verdict="could not check"
            [ -z "$sync_detail" ] || reason="sync attempted anyway — $sync_detail"
            UNCHECKED=$((UNCHECKED + 1))
            finding=1
        elif [ "$list_state" = "failed" ]; then
            verdict="could not check"
            UNCHECKED=$((UNCHECKED + 1))
            finding=1
        elif [ "$sync_state" = "synced" ]; then
            verdict="synced"
            SYNCED=$((SYNCED + 1))
            if [ -z "$artifact_epoch" ]; then
                artifact_clause="artifact: no manifest found (dolt reported success)"
            fi
        else
            # add failed, sync failed or sync timeout. The reason carries
            # the verdict word and the tool's words; the verdict itself is
            # that word, or `missing` when no artifact exists anywhere.
            if [ -n "$add_detail" ]; then
                failure_word="add failed"
                reason="add failed: $add_detail"
            elif [ "$sync_state" = "timeout" ]; then
                failure_word="sync timeout"
                reason="$sync_detail"
            else
                failure_word="sync failed"
                reason="$sync_detail"
            fi
            if [ -z "$artifact_epoch" ]; then
                # No artifact anywhere: never backed up. Counted once, as
                # missing, with the tool's words as the reason.
                verdict="missing"
                MISSING=$((MISSING + 1))
                high=1
            else
                verdict="$failure_word"
                FAILED=$((FAILED + 1))
            fi
            finding=1
        fi
    else
        # verify mode
        if [ "$list_state" != "ok" ]; then
            verdict="could not check"
            [ -z "$artifact_epoch" ] || artifact_clause="$artifact_clause — expected path only, freshness not verified"
            UNCHECKED=$((UNCHECKED + 1))
            finding=1
        elif [ -z "$artifact_epoch" ]; then
            verdict="missing"
            MISSING=$((MISSING + 1))
            high=1
            finding=1
        elif [ "$artifact_age" -le "$STALE_SECS" ]; then
            verdict="fresh"
            VERIFIED=$((VERIFIED + 1))
        else
            # Clock-stale. One bounded read of the HEAD commit time says
            # whether the artifact is behind or merely old. TIMESTAMPDIFF is
            # pure datetime arithmetic: UNIX_TIMESTAMP(date) converts through
            # the session time zone and returned this server's HEAD four
            # hours late, and fractional.
            STALE=$((STALE + 1))
            finding=1
            head_query="SELECT TIMESTAMPDIFF(SECOND,'1970-01-01 00:00:00',date) AS head_epoch FROM \`$db\`.dolt_log LIMIT 1"
            rc=0
            head_epoch=""
            if err_file="$(new_capture_file)"; then
                head_epoch="$(dolt_sql_bounded "$CONTENT_TIMEOUT" -r csv -q "$head_query" 2>"$err_file" | tail -n1)" || rc=$?
                head_err="$(last_err "$err_file")"
                rm -f "$err_file"
            else
                rc=1
                head_err="mktemp failed, cannot capture dolt stderr (check \$TMPDIR)"
            fi
            head_epoch="$(printf '%s' "$head_epoch" | tr -d '[:space:]')"
            if [ "$rc" -eq 0 ]; then
                case "$head_epoch" in
                    ''|*[!0-9]*)
                        rc=1
                        head_err="unexpected content query output: ${head_epoch:-<empty>}"
                        ;;
                esac
            fi
            if [ "$rc" -ne 0 ]; then
                verdict="stale, content: not checked ($(tool_failure "HEAD commit read" "$rc" "$head_err" "$CONTENT_TIMEOUT"))"
                high=1
            elif [ "$head_epoch" -le "$artifact_epoch" ]; then
                verdict="stale (unsynced)"
                reason="content-complete as of last commit $(backup_fmt_epoch "$head_epoch") — nothing to sync"
            else
                verdict="stale, behind"
                reason="commits newer than the last artifact (last commit $(backup_fmt_epoch "$head_epoch"))"
                high=1
            fi
            reason="${reason:+$reason; }$STALE_HINT"
        fi
    fi
    [ "$high" -eq 0 ] || HIGH_RISK=1

    # 7. The entry line, for every database:
    #    <db>: <verdict> — <dest> -> <url>[; also <name> -> <url> (<age>)]; last artifact <ISO-8601 zone> (<age>)[; <reason>][; <hint>]
    #    When no destination could be resolved the first slot says why
    #    (the list timeout or failure, the missing directory).
    if [ -n "$dest" ]; then
        if [ -n "$dest_url" ]; then
            dest_clause="$dest -> $dest_url $(artifact_age_clause "$dest_url" "$now")"
            if [ -n "$artifact_path" ] && [ "$dest_url" = "file://$artifact_path" ]; then
                :
            elif [ -n "$artifact_path" ]; then
                dest_clause="$dest_clause, not the freshest destination"
            fi
        else
            dest_clause="$dest (url not listed)"
        fi
        [ -z "$dest_note" ] || dest_clause="$dest_clause; $dest_note"
    elif [ "$list_state" != "ok" ]; then
        # No destination could be resolved; the slot carries why.
        dest_clause="$list_detail"
    elif [ -n "$listing" ]; then
        dest_clause="no destination at $expected_url"
    else
        dest_clause="no destination configured"
    fi
    also_clause=""
    if [ -n "$listing" ]; then
        while IFS=$'\t' read -r name url; do
            [ -n "$name" ] || continue
            [ "$name" != "$dest" ] || continue
            case "$url" in
                file://*) also_clause="$also_clause; also $name -> $url $(artifact_age_clause "$url" "$now")" ;;
                *) also_clause="$also_clause; also $name -> $url (not a file:// destination, not measured)" ;;
            esac
        done <<EOF
$listing
EOF
    fi
    entry="$db: $verdict — $dest_clause$also_clause; $artifact_clause"
    [ -z "$reason" ] || entry="$entry; $reason"
    echo "$entry"
    [ "$finding" -eq 0 ] || append_finding "$entry"
done

FINDING_COUNT=$((STALE + MISSING + UNCHECKED + FAILED))
OFFSITE_STATUS="skipped"

# --- Step 4: Rsync backup artifacts to offsite storage (sync mode) ---

if [ -n "$OFFSITE_PATH" ] && [ "$GC_BACKUP_MODE" = "sync" ]; then
    if [ ! -d "$BACKUP_ARTIFACT_DIR" ]; then
        OFFSITE_STATUS="missing-artifacts"
    elif same_path "$BACKUP_ARTIFACT_DIR" "$DOLT_DATA_DIR"; then
        OFFSITE_STATUS="invalid-source"
    else
        rc=0
        if err_file="$(new_capture_file)"; then
            run_bounded 300 rsync -a --delete "$BACKUP_ARTIFACT_DIR/" "$OFFSITE_PATH/" 2>"$err_file" || rc=$?
            offsite_err="$(last_err "$err_file")"
            rm -f "$err_file"
        else
            rc=1
            offsite_err="mktemp failed, cannot capture rsync stderr (check \$TMPDIR)"
        fi
        case "$rc" in
            0) OFFSITE_STATUS="ok" ;;
            124|137) OFFSITE_STATUS="$(tool_failure "rsync" "$rc" "$offsite_err" 300)" ;;
            *) OFFSITE_STATUS="failed: ${offsite_err:-<no stderr>} (exit $rc)" ;;
        esac
    fi
fi

# --- Step 5: Report ---

# Severity follows data risk, not the count of plumbing failures: any
# database that is missing, behind, or stale with its content unchecked is
# [HIGH]; so is half or more of the fleet not fresh (the producer is down);
# everything else — a single content-complete unsynced store, say — is
# [MEDIUM].
ESCALATION_SUFFIX=""
if [ "$FINDING_COUNT" -gt 0 ]; then
    severity="MEDIUM"
    if [ "$HIGH_RISK" -eq 1 ] || [ $((FINDING_COUNT * 2)) -ge "$TOTAL" ]; then
        severity="HIGH"
    fi
    subject="Dolt backup: $FINDING_COUNT/$TOTAL databases need attention — $STALE stale, $MISSING missing, $UNCHECKED could not check"
    [ "$FAILED" -eq 0 ] || subject="$subject, $FAILED failed"
    subject="$subject [$severity]"
    body="$FINDINGS"
    case "$OFFSITE_STATUS" in
        ok|skipped) ;;
        *) body="$body
offsite: $OFFSITE_STATUS" ;;
    esac
    if [ "$GC_BACKUP_MODE" = "sync" ]; then
        body="$body
synced: $SYNCED/$TOTAL — every entry is in the order's run output"
    else
        body="$body
fresh: $VERIFIED/$TOTAL — every entry is in the order's run output"
    fi
    if ! send_escalation "$subject" "$body"; then
        ESCALATION_SUFFIX=", escalation: FAILED"
    fi
fi

SUMMARY="backup — synced: $SYNCED/$TOTAL, verified: $VERIFIED/$TOTAL, stale: $STALE, missing: $MISSING, could-not-check: $UNCHECKED"
[ "$FAILED" -eq 0 ] || SUMMARY="$SUMMARY, failed: $FAILED"
SUMMARY="$SUMMARY, offsite: $OFFSITE_STATUS$ESCALATION_SUFFIX"
dolt_notify_done "$SUMMARY"
echo "backup: $SUMMARY"
