//go:build integration || dolt_integration

package dolt_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompactScriptRealDoltRemotePush(t *testing.T) {
	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		t.Skipf("dolt not found: %v", err)
	}

	root := repoRoot(t)
	cityPath := t.TempDir()
	dataDir := filepath.Join(cityPath, ".beads", "dolt")
	dbDir := filepath.Join(dataDir, "beads")
	remoteDir := filepath.Join(t.TempDir(), "remote")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	if err := os.MkdirAll(remoteDir, 0o755); err != nil {
		t.Fatalf("mkdir remote dir: %v", err)
	}

	runDoltForCompactTest(t, doltPath, remoteDir, "init", "--name", "Gas City", "--email", "test@example.com")
	runDoltForCompactTest(t, doltPath, dbDir, "init", "--name", "Gas City", "--email", "test@example.com")
	runDoltForCompactTest(t, doltPath, dbDir, "sql", "-q",
		"CREATE TABLE beads (id int primary key, name varchar(20)); INSERT INTO beads VALUES (1, 'first');")
	runDoltForCompactTest(t, doltPath, dbDir, "add", ".")
	runDoltForCompactTest(t, doltPath, dbDir, "commit", "-m", "seed first bead")
	runDoltForCompactTest(t, doltPath, dbDir, "sql", "-q", "INSERT INTO beads VALUES (2, 'second');")
	runDoltForCompactTest(t, doltPath, dbDir, "commit", "-Am", "seed second bead")
	runDoltForCompactTest(t, doltPath, dbDir, "remote", "add", "origin", "file://"+remoteDir)
	runDoltForCompactTest(t, doltPath, dbDir, "push", "--force", "--set-upstream", "origin", "main")

	port, pid := startRealDoltServerForCompactTest(t, doltPath, dataDir)
	writeManagedRuntimeStateForScriptWithPID(t, cityPath, port, pid)
	waitForDoltServerQueryForCompactTest(t, doltPath, port)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", filepath.Join(root, "commands", "compact", "run.sh"))
	cmd.Env = append(filteredEnv(
		"PATH",
		"GC_CITY_PATH",
		"GC_PACK_DIR",
		"GC_DOLT_DATA_DIR",
		"GC_DOLT_PORT",
		"GC_DOLT_HOST",
		"GC_DOLT_USER",
		"GC_DOLT_PASSWORD",
		"GC_DOLT_MANAGED_LOCAL",
		"GC_DOLT_COMPACT_THRESHOLD_COMMITS",
		"GC_DOLT_COMPACT_CALL_TIMEOUT_SECS",
		"GC_DOLT_COMPACT_PUSH_TIMEOUT_SECS",
	),
		"PATH="+filepath.Dir(doltPath)+":"+os.Getenv("PATH"),
		"GC_CITY_PATH="+cityPath,
		"GC_PACK_DIR="+root,
		"GC_DOLT_DATA_DIR="+dataDir,
		fmt.Sprintf("GC_DOLT_PORT=%d", port),
		"GC_DOLT_HOST=127.0.0.1",
		"GC_DOLT_USER=root",
		"GC_DOLT_PASSWORD=",
		"GC_DOLT_MANAGED_LOCAL=1",
		"GC_DOLT_COMPACT_THRESHOLD_COMMITS=1",
		"GC_DOLT_COMPACT_CALL_TIMEOUT_SECS=20",
		"GC_DOLT_COMPACT_PUSH_TIMEOUT_SECS=20",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compact script failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "remote=origin pushed compacted main") {
		t.Fatalf("compact output missing remote push success:\n%s", out)
	}

	localHead := doltServerHeadForCompactTest(t, doltPath, port)
	cloneParent := t.TempDir()
	runDoltForCompactTest(t, doltPath, cloneParent, "clone", "file://"+remoteDir, "cloned")
	remoteHead := doltHeadForCompactTest(t, doltPath, filepath.Join(cloneParent, "cloned"))
	if localHead != remoteHead {
		t.Fatalf("remote HEAD = %s, want local compacted HEAD %s", remoteHead, localHead)
	}
}

func runDoltForCompactTest(t *testing.T, doltPath, dir string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, doltPath, args...)
	cmd.Dir = dir
	// Newer dolt CLIs colorize `dolt log` output even without a TTY; ANSI
	// escapes would corrupt hash parsing in doltHeadForCompactTest.
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("dolt %s failed in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return string(out)
}

func startRealDoltServerForCompactTest(t *testing.T, doltPath, dataDir string) (int, int) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocating dolt port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("closing dolt port probe: %v", err)
	}

	logPath := filepath.Join(dataDir, "sql-server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create dolt server log: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, doltPath, "sql-server",
		"-H", "127.0.0.1",
		"-P", fmt.Sprintf("%d", port),
		"--data-dir", dataDir,
		"--loglevel", "warning",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatalf("start dolt sql-server: %v", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()
	cleanup := func() {
		cancel()
		select {
		case <-waitCh:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-waitCh
		}
		_ = logFile.Close()
	}
	t.Cleanup(cleanup)
	return port, cmd.Process.Pid
}

func waitForDoltServerQueryForCompactTest(t *testing.T, doltPath string, port int) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var lastOut []byte
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, doltPath,
			"--host", "127.0.0.1",
			"--port", fmt.Sprintf("%d", port),
			"--user", "root",
			"--no-tls",
			"--use-db", "beads",
			"sql", "-q", "SELECT 1",
		)
		cmd.Env = append(filteredEnv("DOLT_CLI_PASSWORD"), "DOLT_CLI_PASSWORD=")
		lastOut, lastErr = cmd.CombinedOutput()
		cancel()
		if lastErr == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("dolt sql-server did not become query-ready on port %d: %v\n%s", port, lastErr, lastOut)
}

func doltHeadForCompactTest(t *testing.T, doltPath, dir string) string {
	t.Helper()
	out := runDoltForCompactTest(t, doltPath, dir, "log", "--oneline", "-n", "1")
	fields := strings.Fields(out)
	if len(fields) == 0 {
		t.Fatalf("empty dolt log output in %s", dir)
	}
	return fields[0]
}

func doltServerHeadForCompactTest(t *testing.T, doltPath string, port int) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, doltPath,
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--user", "root",
		"--no-tls",
		"--use-db", "beads",
		"sql", "-r", "csv", "-q", "SELECT commit_hash FROM dolt_log ORDER BY date DESC LIMIT 1",
	)
	cmd.Env = append(filteredEnv("DOLT_CLI_PASSWORD"), "DOLT_CLI_PASSWORD=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("query server HEAD: %v\n%s", err, out)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[1]) == "" {
		t.Fatalf("unexpected server HEAD output:\n%s", out)
	}
	return strings.TrimSpace(lines[1])
}

// TestCompactMixedDriftProofRealDolt runs mixed_drift_is_writer_only against a
// real dolt sql-server. The mock Dolt cannot show that a revision database
// (--use-db "<db>/<commit>") returns a table's hash as of a past commit, or
// what DOLT_DIFF reports for an added, a modified and a removed row, so this is
// the one place the proof's two probe queries meet a server.
//
// The history is scripted by hand in a throwaway database, so no race is
// needed: a seed, a flatten done with the two statements run.sh itself runs,
// then a writer commit (one row added to events, one row updated in issues),
// then a commit that deletes an issues row.
func TestCompactMixedDriftProofRealDolt(t *testing.T) {
	doltPath, err := exec.LookPath("dolt")
	if err != nil {
		t.Skipf("dolt not found: %v", err)
	}

	root := repoRoot(t)
	workDir := t.TempDir()
	dataDir := filepath.Join(workDir, "dolt")
	dbDir := filepath.Join(dataDir, "beads")
	if err := os.MkdirAll(dbDir, 0o755); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}

	runDoltForCompactTest(t, doltPath, dbDir, "init", "--name", "Gas City", "--email", "test@example.com")
	runDoltForCompactTest(t, doltPath, dbDir, "sql", "-q",
		"CREATE TABLE events (id int primary key, body varchar(40));"+
			"CREATE TABLE issues (id int primary key, title varchar(40));"+
			"INSERT INTO events VALUES (1, 'created');"+
			"INSERT INTO issues VALUES (1, 'first'), (2, 'second');")
	runDoltForCompactTest(t, doltPath, dbDir, "add", ".")
	runDoltForCompactTest(t, doltPath, dbDir, "commit", "-m", "seed tables")
	runDoltForCompactTest(t, doltPath, dbDir, "sql", "-q",
		"INSERT INTO events VALUES (2, 'updated'); INSERT INTO issues VALUES (3, 'third');")
	runDoltForCompactTest(t, doltPath, dbDir, "commit", "-Am", "more history to flatten")

	port, _ := startRealDoltServerForCompactTest(t, doltPath, dataDir)
	waitForDoltServerQueryForCompactTest(t, doltPath, port)
	// From here on every dolt call is a client of the test's server, which has
	// no root password. runDoltForCompactTest passes the process environment
	// through, so say so there rather than let the client prompt for one. It
	// is set only now: the local commands above and the server itself reject
	// a password that comes without a user.
	t.Setenv("DOLT_CLI_PASSWORD", "")

	// Pre-flight, in run.sh's format: <table> <row count> <DOLT_HASHOF_TABLE>.
	preflightHash := map[string]string{}
	var preflight strings.Builder
	for _, table := range []string{"events", "issues"} {
		count := doltServerCellForCompactTest(t, doltPath, port, "beads", "SELECT COUNT(*) FROM `"+table+"`")
		hash := doltServerCellForCompactTest(t, doltPath, port, "beads", "SELECT DOLT_HASHOF_TABLE('"+table+"')")
		if hash == "" {
			t.Fatalf("empty pre-flight hash for %s", table)
		}
		preflightHash[table] = hash
		fmt.Fprintf(&preflight, "%s %s %s\n", table, count, hash)
	}
	preflightPath := filepath.Join(workDir, "preflight")
	if err := os.WriteFile(preflightPath, []byte(preflight.String()), 0o600); err != nil {
		t.Fatalf("write pre-flight file: %v", err)
	}

	// The flatten, with the statements and the single invocation run.sh uses.
	rootCommit := doltServerCellForCompactTest(t, doltPath, port, "beads",
		"SELECT commit_hash FROM dolt_log ORDER BY date ASC LIMIT 1")
	doltServerExecForCompactTest(t, doltPath, port, "beads",
		"CALL DOLT_RESET('--soft', '"+rootCommit+"'); CALL DOLT_COMMIT('-Am', 'compaction: flatten history');")
	flattenHead := doltServerHeadForCompactTest(t, doltPath, port)
	if flattenHead == rootCommit {
		t.Fatalf("flatten did not create a commit: HEAD is still the root %s", rootCommit)
	}

	// The writer: what one ordinary bead write does, after the flatten.
	doltServerExecForCompactTest(t, doltPath, port, "beads",
		"INSERT INTO events VALUES (3, 'closed'); UPDATE issues SET title = 'first, edited' WHERE id = 1; CALL DOLT_COMMIT('-Am', 'writer after the flatten');")
	writerHead := doltServerHeadForCompactTest(t, doltPath, port)
	if writerHead == flattenHead {
		t.Fatalf("writer commit did not move HEAD past the flatten %s", flattenHead)
	}
	// The mixed signal verify_counts would see: both tables' working-set
	// hashes now differ from pre-flight.
	for _, table := range []string{"events", "issues"} {
		now := doltServerCellForCompactTest(t, doltPath, port, "beads", "SELECT DOLT_HASHOF_TABLE('"+table+"')")
		if now == preflightHash[table] {
			t.Fatalf("table %s did not drift after the writer commit; the test proves nothing", table)
		}
	}

	out, err := runMixedDriftProofForCompactTest(t, doltPath, root, port, preflightPath, flattenHead, writerHead)
	if err != nil {
		t.Fatalf("proof should pass for a writer that only added and modified rows: %v\n%s", err, out)
	}
	if strings.Contains(out, "mixed-drift proof failed") {
		t.Fatalf("a passing proof must not print a failure line:\n%s", out)
	}

	// A flatten head that is not the flatten: the pre-flight hashes cannot
	// match a commit that already holds the writer's rows.
	out, err = runMixedDriftProofForCompactTest(t, doltPath, root, port, preflightPath, writerHead, flattenHead)
	if err == nil || !strings.Contains(out, "item=flatten_preserved table=events") {
		t.Fatalf("proof should fail at flatten_preserved when the commit holds the writer's rows: err=%v\n%s", err, out)
	}

	// A later commit that deletes a row must fail the proof.
	doltServerExecForCompactTest(t, doltPath, port, "beads",
		"DELETE FROM issues WHERE id = 2; CALL DOLT_COMMIT('-Am', 'writer deletes a row');")
	deleteHead := doltServerHeadForCompactTest(t, doltPath, port)
	out, err = runMixedDriftProofForCompactTest(t, doltPath, root, port, preflightPath, flattenHead, deleteHead)
	if err == nil {
		t.Fatalf("proof passed although a row was removed after the flatten:\n%s", out)
	}
	if !strings.Contains(out, "mixed-drift proof failed item=no_removed_rows table=issues") {
		t.Fatalf("proof should fail at no_removed_rows for issues:\n%s", out)
	}
}

// runMixedDriftProofForCompactTest sources the proof helper into a small sh
// harness whose query_single_cell uses the dolt flags run.sh's dolt_query uses,
// and calls mixed_drift_is_writer_only for tables events and issues. It returns
// the combined output; a non-nil error is a failed proof (exit 1) or a harness
// fault (any other exit).
func runMixedDriftProofForCompactTest(t *testing.T, doltPath, packRoot string, port int, preflightPath, flattenHead, postVerifyHead string) (string, error) {
	t.Helper()
	harness := filepath.Join(t.TempDir(), "proof-harness.sh")
	if err := os.WriteFile(harness, []byte(`#!/bin/sh
set -eu
dolt_query() {
  db="$1"
  query="$2"
  export DOLT_CLI_PASSWORD=""
  "$PROOF_DOLT" --host 127.0.0.1 --port "$PROOF_PORT" \
    --user root --no-tls \
    --use-db "$db" \
    sql -r tabular -q "$query"
}
query_single_cell() {
  db="$1"
  failure_message="$2"
  query="$3"
  out_tmp=$(mktemp)
  err_tmp=$(mktemp)
  if ! dolt_query "$db" "$query" > "$out_tmp" 2>"$err_tmp"; then
    printf 'compact: db=%s %s\n' "$db" "$failure_message" >&2
    cat "$err_tmp" >&2
    rm -f "$out_tmp" "$err_tmp"
    return 1
  fi
  awk 'NR==4 {gsub(/[| ]/, ""); print; exit}' "$out_tmp"
  rm -f "$out_tmp" "$err_tmp"
}
valid_table_name() {
  case "$1" in
    ''|*[!A-Za-z0-9_]*) return 1 ;;
    *) return 0 ;;
  esac
}
. "$PROOF_LIB"
db=beads
rc=0
mixed_drift_is_writer_only "$db" "$PROOF_PREFLIGHT" "$PROOF_FLATTEN_HEAD" "$PROOF_POST_VERIFY_HEAD" " events  issues" || rc=1
if [ "$db" != "beads" ]; then
  printf 'harness: the proof rewrote the caller db to %s\n' "$db" >&2
  exit 3
fi
exit "$rc"
`), 0o700); err != nil {
		t.Fatalf("write proof harness: %v", err)
	}
	out, err := newShScriptCmd(harness, append(filteredEnv(),
		"PROOF_DOLT="+doltPath,
		fmt.Sprintf("PROOF_PORT=%d", port),
		"PROOF_LIB="+filepath.Join(packRoot, "assets", "scripts", "compact-gain-drift-proof.sh"),
		"PROOF_PREFLIGHT="+preflightPath,
		"PROOF_FLATTEN_HEAD="+flattenHead,
		"PROOF_POST_VERIFY_HEAD="+postVerifyHead,
	)).CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 1 {
		t.Fatalf("proof harness fault (exit %d):\n%s", exitErr.ExitCode(), out)
	}
	return string(out), err
}

// doltServerCellForCompactTest returns the single cell a query yields from the
// test's dolt sql-server.
func doltServerCellForCompactTest(t *testing.T, doltPath string, port int, db, query string) string {
	t.Helper()
	out := doltServerQueryForCompactTest(t, doltPath, port, db, "csv", query)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("query %q returned no row:\n%s", query, out)
	}
	return strings.TrimSpace(lines[1])
}

// doltServerExecForCompactTest runs statements against the test's dolt
// sql-server and discards the result.
func doltServerExecForCompactTest(t *testing.T, doltPath string, port int, db, statements string) {
	t.Helper()
	doltServerQueryForCompactTest(t, doltPath, port, db, "tabular", statements)
}

// doltServerQueryForCompactTest sends a query to the test's dolt sql-server
// with the connection flags run.sh's dolt_query uses, and fails the test if
// the client exits non-zero. The caller must have cleared DOLT_CLI_PASSWORD.
func doltServerQueryForCompactTest(t *testing.T, doltPath string, port int, db, format, query string) string {
	t.Helper()
	return runDoltForCompactTest(t, doltPath, t.TempDir(),
		"--host", "127.0.0.1",
		"--port", fmt.Sprintf("%d", port),
		"--user", "root",
		"--no-tls",
		"--use-db", db,
		"sql", "-r", format, "-q", query,
	)
}
