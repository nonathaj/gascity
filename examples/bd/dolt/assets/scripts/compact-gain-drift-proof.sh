#!/bin/sh
# compact-gain-drift-proof.sh — Option A row-preservation proof for the
# post-flatten gain+drift case (gastownhall/gascity#2846).
#
# When verify_counts sees a table gain rows AND its value hash drift, the
# safety property at stake ("pre-flight rows remain reachable") cannot be
# inferred from HEAD movement alone: a concurrent writer whose commit is
# ABSORBED into the flatten commit moves no HEAD, so the HEAD-proven gate
# misses it and a benign race is hard-quarantined — which then blocks all
# future GC of a busy DB (the memory-exhaustion failure the code calls out).
#
# This proves preservation DIRECTLY: for each gained+drifted table, diff the
# pre-flight snapshot HEAD against the flatten commit. If the only change is
# `added` rows (no `removed`/`modified`), every pre-flight row survived and the
# gain is concurrent-writer data — defer, exactly as the HEAD-proven path does.
# It is strictly more rigorous than the HEAD proxy: it proves reachability
# instead of inferring it. Any removed/modified row, or any probe failure,
# fails closed and falls through to quarantine.
#
# Depends on `query_single_cell` and `valid_table_name` from run.sh.

# diff_is_additive_only <db> <from_head> <to_head> <table>
# Returns 0 iff the table's <from>..<to> content diff contains only `added`
# rows. Returns non-zero (fail closed) if either commit endpoint is missing,
# the table name is missing or invalid, the diff probe fails or returns a
# non-numeric result, or the table shows removed/modified rows. Shared by the
# gain+drift preservation proof and the committed-root drift proof's
# first-committed table case (run.sh db_root_drift_within_verified_tables).
diff_is_additive_only() {
  _da_db="$1"
  _da_from="$2"
  _da_to="$3"
  _da_t="$4"
  # Without both commit endpoints there is nothing to diff against — fail closed.
  [ -n "$_da_from" ] && [ -n "$_da_to" ] && [ -n "$_da_t" ] || return 1
  valid_table_name "$_da_t" || return 1
  # Count rows that are NOT purely additive between the two commits. Zero means
  # every row present at <from> is reachable unchanged at <to> and the only
  # change was added rows.
  if ! _da_nonadded=$(query_single_cell "$_da_db" \
    "preservation diff probe failed for table=$_da_t" \
    "SELECT COUNT(*) FROM DOLT_DIFF('$_da_from', '$_da_to', '$_da_t') WHERE diff_type <> 'added'"); then
    return 1
  fi
  case "$_da_nonadded" in
    0) return 0 ;;             # only added rows — this table's <from> rows preserved
    ''|*[!0-9]*) return 1 ;;   # empty/non-numeric probe result — fail closed
    *) return 1 ;;             # one or more removed/modified rows — not preservable
  esac
}

# gain_drift_is_additive_only <db> <from_head> <to_head> <space-separated tables>
# Returns 0 iff every listed table's <from>..<to> content diff contains only
# `added` rows. Returns non-zero (fail closed) if the table list is empty or
# any table fails diff_is_additive_only.
gain_drift_is_additive_only() {
  _gd_db="$1"
  _gd_from="$2"
  _gd_to="$3"
  _gd_tables="$4"
  _gd_seen=0
  for _gd_t in $_gd_tables; do
    _gd_seen=1
    diff_is_additive_only "$_gd_db" "$_gd_from" "$_gd_to" "$_gd_t" || return 1
  done
  # An empty table list is not a proof of preservation.
  [ "$_gd_seen" = "1" ] || return 1
  return 0
}

# --- mixed-drift writer-race proof -------------------------------------------
#
# An ordinary bead write that lands AFTER the flatten commit shows up in
# verify_counts as two failure categories at once: rows gained on one table
# (an event row) and same-count value drift on another (the updated issue).
# Each single-category defer path in run.sh excludes the other category, so
# the commonest write there is was quarantined every time it raced a flatten.
#
# mixed_drift_is_writer_only proves that case benign with three facts:
#
#   1. writer_proven      HEAD moved past the flatten's own commit, so a
#                         writer committed after the flatten.
#   2. flatten_preserved  each drifted table's value hash AT the flatten commit
#                         equals its pre-flight hash. The flatten left the
#                         table exactly as pre-flight had it, so all the drift
#                         belongs to the commits after it.
#   3. no_removed_rows    those commits removed no row from a drifted table.
#
# Item 2 is exact where diff_is_additive_only is deliberately loose (it allows
# added rows, the right rule for a writer ABSORBED into the flatten). An
# absorbed writer changes the table at the flatten commit, fails item 2 here,
# and stays quarantined.

# valid_commit_ref <ref>
# Returns 0 iff <ref> is a non-empty string of digits and lowercase letters.
# This is an injection guard, not a format check: the ref is placed in
# `--use-db "<db>/<ref>"` and in a quoted SQL literal, so `/`, quotes,
# whitespace and every other character are refused. A well-formed ref that
# does not resolve makes the probe fail, which fails closed. The alphabet is
# spelled out because a `[a-z]` range matches uppercase in some locales.
valid_commit_ref() {
  case "$1" in
    ''|*[!0123456789abcdefghijklmnopqrstuvwxyz]*) return 1 ;;
    *) return 0 ;;
  esac
}

# table_value_hash_at <db> <commit> <table>
# Prints the table's value hash as of <commit>. DOLT_HASHOF_TABLE takes one
# argument and reads the current working set, so the commit is selected with a
# revision database (`--use-db "<db>/<commit>"`). Returns non-zero without
# sending a probe when the commit or table name is invalid.
#
# Call this only through $( … ): query_single_cell assigns the global `db`,
# and a direct call would leave the caller's `db` set to "<db>/<commit>".
table_value_hash_at() {
  _th_db="$1"
  _th_commit="$2"
  _th_t="$3"
  valid_commit_ref "$_th_commit" || return 1
  [ -n "$_th_t" ] || return 1
  valid_table_name "$_th_t" || return 1
  query_single_cell "$_th_db/$_th_commit" \
    "table value hash probe at commit=$_th_commit failed for table=$_th_t" \
    "SELECT DOLT_HASHOF_TABLE('$_th_t')"
}

# preflight_table_hash <preflight_file> <table>
# Prints the value hash recorded for <table> in run.sh's pre-flight file, whose
# lines are `<table> <row count> <hash>`. Reads a line the way verify_counts
# does and matches the table name exactly, never as a prefix. Returns non-zero
# when the file is unreadable, the table has no line, or its line has no hash.
preflight_table_hash() {
  _ph_file="$1"
  _ph_t="$2"
  [ -r "$_ph_file" ] || return 1
  while IFS= read -r _ph_line; do
    [ -n "$_ph_line" ] || continue
    [ "${_ph_line%% *}" = "$_ph_t" ] || continue
    _ph_rest=${_ph_line#* }
    case "$_ph_rest" in
      *" "*) ;;
      *) return 1 ;;
    esac
    _ph_hash=${_ph_rest#* }
    [ -n "$_ph_hash" ] || return 1
    printf '%s' "$_ph_hash"
    return 0
  done < "$_ph_file"
  return 1
}

# mixed_drift_proof_failed <db> <item> <table|-> <detail>
# The one stderr line a failed proof prints, so an operator reading a
# quarantine can see which fact was missing.
mixed_drift_proof_failed() {
  printf 'compact: db=%s mixed-drift proof failed item=%s table=%s %s\n' \
    "$1" "$2" "$3" "$4" >&2
}

# mixed_drift_is_writer_only <db> <preflight_file> <flatten_head> <post_verify_head> <space-separated tables>
# Returns 0 iff the three facts above all hold for every listed table. The
# items are checked in order and the first failure ends the proof, so no probe
# is sent when writer_proven or table_set fails. Every failure prints one
# `mixed-drift proof failed item=<item> table=<table|-> <detail>` line on
# stderr and returns 1: an empty, invalid or unmoved head, an empty table list,
# an invalid table name, a table missing from the pre-flight file, a probe that
# fails or returns nothing, a hash that differs, or any diff row that is
# neither `added` nor `modified` (a removed row, or a diff type this script
# does not know).
mixed_drift_is_writer_only() {
  _mx_db="$1"
  _mx_pre="$2"
  _mx_flat="$3"
  _mx_post="$4"
  _mx_tables="$5"

  if ! valid_commit_ref "$_mx_flat"; then
    mixed_drift_proof_failed "$_mx_db" writer_proven - "flatten_HEAD is empty or not a plain commit ref"
    return 1
  fi
  if ! valid_commit_ref "$_mx_post"; then
    mixed_drift_proof_failed "$_mx_db" writer_proven - "post_verify_HEAD is empty or not a plain commit ref"
    return 1
  fi
  if [ "$_mx_post" = "$_mx_flat" ]; then
    mixed_drift_proof_failed "$_mx_db" writer_proven - "post_verify_HEAD equals flatten_HEAD=$_mx_flat; no writer commit after the flatten"
    return 1
  fi

  _mx_seen=0
  for _mx_t in $_mx_tables; do
    _mx_seen=1
    if ! valid_table_name "$_mx_t"; then
      mixed_drift_proof_failed "$_mx_db" table_set "$_mx_t" "invalid table name"
      return 1
    fi
  done
  if [ "$_mx_seen" != "1" ]; then
    mixed_drift_proof_failed "$_mx_db" table_set - "empty table list is not a proof"
    return 1
  fi

  for _mx_t in $_mx_tables; do
    if ! _mx_want=$(preflight_table_hash "$_mx_pre" "$_mx_t"); then
      mixed_drift_proof_failed "$_mx_db" flatten_preserved "$_mx_t" "no pre-flight hash recorded for this table"
      return 1
    fi
    if ! _mx_got=$(table_value_hash_at "$_mx_db" "$_mx_flat" "$_mx_t"); then
      mixed_drift_proof_failed "$_mx_db" flatten_preserved "$_mx_t" "hash probe at flatten_HEAD=$_mx_flat failed"
      return 1
    fi
    if [ -z "$_mx_got" ]; then
      mixed_drift_proof_failed "$_mx_db" flatten_preserved "$_mx_t" "hash probe at flatten_HEAD=$_mx_flat returned an empty value"
      return 1
    fi
    if [ "$_mx_got" != "$_mx_want" ]; then
      mixed_drift_proof_failed "$_mx_db" flatten_preserved "$_mx_t" "hash at flatten_HEAD=$_mx_flat is $_mx_got, pre-flight was $_mx_want"
      return 1
    fi
  done

  for _mx_t in $_mx_tables; do
    if ! _mx_gone=$(query_single_cell "$_mx_db" \
      "mixed-drift removed-row probe failed for table=$_mx_t" \
      "SELECT COUNT(*) FROM DOLT_DIFF('$_mx_flat', '$_mx_post', '$_mx_t') WHERE diff_type NOT IN ('added', 'modified')"); then
      mixed_drift_proof_failed "$_mx_db" no_removed_rows "$_mx_t" "diff probe DOLT_DIFF($_mx_flat..$_mx_post) failed"
      return 1
    fi
    case "$_mx_gone" in
      0) ;;
      ''|*[!0-9]*)
        mixed_drift_proof_failed "$_mx_db" no_removed_rows "$_mx_t" "diff probe DOLT_DIFF($_mx_flat..$_mx_post) returned a non-numeric result"
        return 1
        ;;
      *)
        mixed_drift_proof_failed "$_mx_db" no_removed_rows "$_mx_t" "DOLT_DIFF($_mx_flat..$_mx_post) has $_mx_gone row(s) that are neither added nor modified"
        return 1
        ;;
    esac
  done
  return 0
}
