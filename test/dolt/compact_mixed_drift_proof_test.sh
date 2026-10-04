#!/bin/sh
# Unit test for mixed_drift_is_writer_only (the mixed-drift writer-race proof).
# Lib under test: examples/bd/dolt/assets/scripts/compact-gain-drift-proof.sh
#
# Stubs the run.sh-provided dependencies (query_single_cell, valid_table_name)
# so the proof's decision logic is exercised without a live Dolt server. The
# stub logs every probe to a file, because the helper calls it through $( … )
# and a subshell cannot report back through variables. The two probe queries
# themselves meet a real server in TestCompactMixedDriftProofRealDolt.
set -u

HERE=$(unset CDPATH; cd -- "$(dirname "$0")" && pwd)
LIB="$HERE/../../examples/bd/dolt/assets/scripts/compact-gain-drift-proof.sh"
[ -f "$LIB" ] || { echo "FAIL: lib not found at $LIB"; exit 1; }

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
PROBES="$TMP/probes.log"
ERR="$TMP/stderr.log"
PRE="$TMP/preflight"

# The stub_* variables are read through eval in the stub below.
# shellcheck disable=SC2034
: "${stub_hash_issues:=}" "${stub_removed_issues:=}" "${stub_removed_events:=}"
unset stub_hash_issues stub_removed_issues stub_removed_events

# --- stubs for run.sh-provided helpers --------------------------------------
# query_single_cell <db> <msg> <query>. Like the real one it assigns the global
# `db`, which is why the helper may only call it through $( … ).
#   DOLT_HASHOF_TABLE('<t>')  -> stub_hash_<t>, default "h-<t>"
#   DOLT_DIFF(…, '<t>')       -> stub_removed_<t>, default 0
# STUB_HASH_FAIL / STUB_HASH_EMPTY / STUB_DIFF_FAIL / STUB_DIFF_EMPTY /
# STUB_DIFF_NONNUM name the table whose probe misbehaves.
query_single_cell() {
  db="$1"
  printf 'db=%s query=%s\n' "$1" "$3" >> "$PROBES"
  case "$3" in
    *DOLT_HASHOF_TABLE*)
      _t=$(printf '%s\n' "$3" | sed -n "s/.*DOLT_HASHOF_TABLE('\([^']*\)').*/\1/p")
      [ "${STUB_HASH_FAIL:-}" = "$_t" ] && return 1
      [ "${STUB_HASH_EMPTY:-}" = "$_t" ] && return 0
      _v=""
      eval "_v=\${stub_hash_$_t:-h-$_t}"
      printf '%s' "$_v"
      ;;
    *DOLT_DIFF*)
      _t=$(printf '%s\n' "$3" | sed -n "s/.*DOLT_DIFF('[^']*', *'[^']*', *'\([^']*\)').*/\1/p")
      [ "${STUB_DIFF_FAIL:-}" = "$_t" ] && return 1
      [ "${STUB_DIFF_EMPTY:-}" = "$_t" ] && return 0
      if [ "${STUB_DIFF_NONNUM:-}" = "$_t" ]; then
        printf 'NULL'
        return 0
      fi
      _v=""
      eval "_v=\${stub_removed_$_t:-0}"
      printf '%s' "$_v"
      ;;
    *)
      return 1
      ;;
  esac
  return 0
}
valid_table_name() {
  [ "${STUB_INVALID_TABLE:-}" = "$1" ] && return 1
  return 0
}

# shellcheck disable=SC1090  # $LIB path is computed from the test's own location
. "$LIB"

# --- harness ----------------------------------------------------------------
pass=0
fail=0
ok() { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
no() {
  fail=$((fail + 1))
  printf 'FAIL - %s\n' "$1"
  [ -s "$ERR" ] && sed 's/^/       stderr: /' "$ERR"
  [ -s "$PROBES" ] && sed 's/^/       probe:  /' "$PROBES"
  return 0
}
reset() {
  unset STUB_HASH_FAIL STUB_HASH_EMPTY STUB_DIFF_FAIL STUB_DIFF_EMPTY \
    STUB_DIFF_NONNUM STUB_INVALID_TABLE \
    stub_hash_events stub_hash_issues stub_hash_issues_archive \
    stub_removed_events stub_removed_issues 2>/dev/null || true
  : > "$PROBES"
  : > "$ERR"
  # run.sh's pre-flight format: <table> <row count> <DOLT_HASHOF_TABLE>
  printf 'events 10 h-events\nissues 7 h-issues\n' > "$PRE"
}
# prove <flatten_head> <post_verify_head> <tables>: run the proof, stderr to $ERR.
prove() {
  mixed_drift_is_writer_only beads "$PRE" "$1" "$2" "$3" 2>"$ERR"
}
probes() { wc -l < "$PROBES" | tr -d ' '; }
# expect_pass <name>; expect_fail <name> <item> [<table>]; both read $? of prove.
expect_pass() {
  if [ "$rc" -eq 0 ] && [ ! -s "$ERR" ]; then ok "$1"; else no "$1 (rc=$rc)"; fi
}
expect_fail() {
  _want="mixed-drift proof failed item=$2"
  [ -n "${3:-}" ] && _want="$_want table=$3"
  if [ "$rc" -ne 0 ] && grep -q "^compact: db=beads $_want " "$ERR" && [ "$(wc -l < "$ERR" | tr -d ' ')" = "1" ]; then
    ok "$1"
  else
    no "$1 (rc=$rc, wanted one line: $_want)"
  fi
}

F=flat0head
P=post0head

# 1. two tables, hashes equal, zero removed -> defer
reset
prove "$F" "$P" "events issues"; rc=$?
expect_pass "1 two tables, flatten preserved, zero removed -> defer"

# 2. modified and added rows after the flatten are allowed: the probe counts
#    only what is neither, so a writer's INSERT and UPDATE read as zero.
reset
prove "$F" "$P" "events issues"; rc=$?
if [ "$rc" -eq 0 ] && grep -q "DOLT_DIFF('$F', '$P', 'issues') WHERE diff_type NOT IN ('added', 'modified')" "$PROBES"; then
  ok "2 added and modified rows are allowed; the probe counts only the rest"
else
  no "2 added and modified rows are allowed; the probe counts only the rest (rc=$rc)"
fi

# 3. one removed row in one table -> quarantine
reset; stub_removed_issues=1
prove "$F" "$P" "events issues"; rc=$?
expect_fail "3 one removed row -> quarantine" no_removed_rows issues

# 4. the flatten changed one table -> quarantine
reset; stub_hash_issues=h-issues-changed
prove "$F" "$P" "events issues"; rc=$?
expect_fail "4 hash at flatten_head differs -> quarantine" flatten_preserved issues

# 5. HEAD did not move past the flatten -> no writer proven
reset
prove "$F" "$F" "events issues"; rc=$?
expect_fail "5 post_verify_head == flatten_head -> quarantine" writer_proven -
if [ "$(probes)" = "0" ]; then ok "5 …and no probe is sent"; else no "5 …and no probe is sent"; fi

# 6. empty heads -> fail closed
reset
prove "$F" "" "events issues"; rc=$?
expect_fail "6 empty post_verify_head -> quarantine" writer_proven -
reset
prove "" "$P" "events issues"; rc=$?
expect_fail "6 empty flatten_head -> quarantine" writer_proven -

# 7. a head that is not a plain commit ref is refused before any probe. It
#    would otherwise reach --use-db "<db>/<ref>" and a quoted SQL literal.
for bad in "ab'cd" "ab/cd" "ab cd" "abCd"; do
  reset
  prove "$bad" "$P" "events issues"; rc=$?
  expect_fail "7 invalid flatten_head [$bad] -> quarantine" writer_proven -
  if [ "$(probes)" = "0" ]; then ok "7 …no probe for flatten_head [$bad]"; else no "7 …no probe for flatten_head [$bad]"; fi
  reset
  prove "$F" "$bad" "events issues"; rc=$?
  expect_fail "7 invalid post_verify_head [$bad] -> quarantine" writer_proven -
  if [ "$(probes)" = "0" ]; then ok "7 …no probe for post_verify_head [$bad]"; else no "7 …no probe for post_verify_head [$bad]"; fi
done

# 8. hash probe fails / returns empty -> fail closed
reset; STUB_HASH_FAIL=events
prove "$F" "$P" "events issues"; rc=$?
expect_fail "8 hash probe failure -> quarantine" flatten_preserved events
reset; STUB_HASH_EMPTY=issues
prove "$F" "$P" "events issues"; rc=$?
expect_fail "8 empty hash probe result -> quarantine" flatten_preserved issues

# 9. diff probe fails / returns empty / returns non-numeric -> fail closed
reset; STUB_DIFF_FAIL=events
prove "$F" "$P" "events issues"; rc=$?
expect_fail "9 diff probe failure -> quarantine" no_removed_rows events
reset; STUB_DIFF_EMPTY=issues
prove "$F" "$P" "events issues"; rc=$?
expect_fail "9 empty diff probe result -> quarantine" no_removed_rows issues
reset; STUB_DIFF_NONNUM=issues
prove "$F" "$P" "events issues"; rc=$?
expect_fail "9 non-numeric diff probe result -> quarantine" no_removed_rows issues

# 10. invalid table name -> fail closed, before any probe
reset; STUB_INVALID_TABLE=issues
prove "$F" "$P" "events issues"; rc=$?
expect_fail "10 invalid table name -> quarantine" table_set issues
if [ "$(probes)" = "0" ]; then ok "10 …and no probe is sent"; else no "10 …and no probe is sent"; fi

# 11. empty table list is not a proof
reset
prove "$F" "$P" ""; rc=$?
expect_fail "11 empty table list -> quarantine" table_set -
reset
prove "$F" "$P" "   "; rc=$?
expect_fail "11 blank table list -> quarantine" table_set -

# 12. a drifted table the pre-flight file never recorded -> fail closed
reset
printf 'events 10 h-events\n' > "$PRE"
prove "$F" "$P" "events issues"; rc=$?
expect_fail "12 table absent from the pre-flight file -> quarantine" flatten_preserved issues
reset
rm -f "$PRE"
prove "$F" "$P" "events issues"; rc=$?
expect_fail "12 missing pre-flight file -> quarantine" flatten_preserved events

# 13. the table's own line is used, not a line it is a prefix of. Whichever
#     order the file lists them in, `issues` must read h-issues.
reset
printf 'issues_archive 3 h-other\nissues 7 h-issues\nevents 10 h-events\n' > "$PRE"
prove "$F" "$P" "events issues"; rc=$?
expect_pass "13 issues is matched exactly, not as a prefix of issues_archive"
reset
printf 'issues_archive 3 h-issues\nevents 10 h-events\n' > "$PRE"
prove "$F" "$P" "events issues"; rc=$?
expect_fail "13 …and issues_archive's line never stands in for issues" flatten_preserved issues

# 14. the caller's `db` is untouched, on a pass and on every kind of failure
reset; db=caller-db
prove "$F" "$P" "events issues"; rc=$?
if [ "$rc" -eq 0 ] && [ "$db" = "caller-db" ]; then ok "14 db unchanged after a pass"; else no "14 db unchanged after a pass (db=$db)"; fi
reset; db=caller-db; stub_removed_events=2
prove "$F" "$P" "events issues"; rc=$?
if [ "$rc" -ne 0 ] && [ "$db" = "caller-db" ]; then ok "14 db unchanged after a diff failure"; else no "14 db unchanged after a diff failure (db=$db)"; fi
reset; db=caller-db; STUB_HASH_FAIL=events
prove "$F" "$P" "events issues"; rc=$?
if [ "$rc" -ne 0 ] && [ "$db" = "caller-db" ]; then ok "14 db unchanged after a hash probe failure"; else no "14 db unchanged after a hash probe failure (db=$db)"; fi

# 15. the hash probe goes to the revision database, the diff probe to the db
reset
prove "$F" "$P" "events issues"; rc=$?
want="db=beads/$F query=SELECT DOLT_HASHOF_TABLE('events')
db=beads/$F query=SELECT DOLT_HASHOF_TABLE('issues')
db=beads query=SELECT COUNT(*) FROM DOLT_DIFF('$F', '$P', 'events') WHERE diff_type NOT IN ('added', 'modified')
db=beads query=SELECT COUNT(*) FROM DOLT_DIFF('$F', '$P', 'issues') WHERE diff_type NOT IN ('added', 'modified')"
if [ "$rc" -eq 0 ] && [ "$(cat "$PROBES")" = "$want" ]; then
  ok "15 hash probes use db/<flatten_head>; diff probes use db; four probes in order"
else
  no "15 hash probes use db/<flatten_head>; diff probes use db; four probes in order"
fi

# 16. the mock Dolt's head values are accepted (they are not 32-char hashes)
reset
prove compactcommit writercommit "events issues"; rc=$?
expect_pass "16 heads compactcommit/writercommit -> defer"

# valid_commit_ref on its own: the injection guard, not a format check
for good in a 0 compactcommit 0123456789abcdefghijklmnopqrstuv; do
  if valid_commit_ref "$good"; then ok "valid_commit_ref accepts [$good]"; else no "valid_commit_ref accepts [$good]"; fi
done
for bad in "" "A" "ab-cd" "ab_cd" "ab.cd" "ab;cd" 'ab"cd' "ab\`cd" "HEAD~1" "ab
cd"; do
  if valid_commit_ref "$bad"; then no "valid_commit_ref refuses [$bad]"; else ok "valid_commit_ref refuses [$bad]"; fi
done

# table_value_hash_at refuses a bad commit or table without sending a probe
reset
if out=$(table_value_hash_at beads "$F" events) && [ "$out" = "h-events" ]; then ok "table_value_hash_at returns the hash"; else no "table_value_hash_at returns the hash"; fi
reset
if out=$(table_value_hash_at beads "ab/cd" events); then no "table_value_hash_at refuses a bad commit"; else ok "table_value_hash_at refuses a bad commit"; fi
if [ "$(probes)" = "0" ]; then ok "…and sends no probe"; else no "…and sends no probe"; fi
reset; STUB_INVALID_TABLE=events
if out=$(table_value_hash_at beads "$F" events); then no "table_value_hash_at refuses a bad table"; else ok "table_value_hash_at refuses a bad table"; fi
if [ "$(probes)" = "0" ]; then ok "…and sends no probe"; else no "…and sends no probe"; fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
