package beads

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/pathutil"
)

func dsnPragmas(t *testing.T, dsn string) []string {
	t.Helper()
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parsing DSN %q: %v", dsn, err)
	}
	return parsed.Query()["_pragma"]
}

// sqliteTuningPragma is the one pragma this store tunes away from the SQLite
// default. Everything else on the DSN predates the tuning work.
const sqliteTuningPragma = "mmap_size(268435456)"

func TestSQLiteStoreDSNCarriesTuningPragmas(t *testing.T) {
	cases := []struct {
		name     string
		dsn      string
		wantMode string
	}{
		{name: "read-write", dsn: sqliteStoreDSN("/city/.gc/store/graph/beads.sqlite", false)},
		{name: "read-only", dsn: sqliteStoreDSN("/city/.gc/store/graph/beads.sqlite", true), wantMode: "ro"},
		{name: "private recovery", dsn: sqliteStorePrivateRecoveryDSN("/snapshot/.beads/beads.sqlite"), wantMode: "rw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pragmas := dsnPragmas(t, tc.dsn)
			for _, pragma := range []string{"busy_timeout(5000)", "foreign_keys(1)", sqliteTuningPragma} {
				if !slices.Contains(pragmas, pragma) {
					t.Errorf("DSN %s is missing _pragma=%s (got %v)", tc.dsn, pragma, pragmas)
				}
			}
			parsed, err := url.Parse(tc.dsn)
			if err != nil {
				t.Fatalf("parsing DSN %q: %v", tc.dsn, err)
			}
			if got := parsed.Query().Get("mode"); got != tc.wantMode {
				t.Errorf("DSN mode = %q, want %q", got, tc.wantMode)
			}
		})
	}
}

// sqliteMeasuredOutPragmas are the pragmas that were tried on this DSN and
// removed, each with the reason a future reader needs before re-adding it from
// general SQLite advice. The reason is in the failure message deliberately: the
// point of this test is not to freeze a pragma list, it is to make sure the
// next person to reach for one of these reads the measurement first.
var sqliteMeasuredOutPragmas = map[string]string{
	"cache_size": "the page cache is per-connection, so this DSN charges it 9x " +
		"(1 writer + 8 pooled readers): cache_size(-64000) measured 1.19 GB of anonymous " +
		"memory and was SLOWER than no pragma at all, because every page-cache allocation " +
		"goes through modernc/libc's one global allocator mutex that all 8 readers share",
	"temp_store": "modernc skips the sorter's bump arena when the temp store is in memory, " +
		"so temp_store(MEMORY) makes every sorter record an individual malloc behind that same " +
		"global mutex: concurrent Ready() measured ~1.8x SLOWER than no pragma at all",
	"synchronous": "NORMAL lets a host crash discard the WAL tail, which regresses " +
		"recoverSequence's MAX-suffix allocator and reissues bead IDs that already escaped this " +
		"store; graph.seqfloor does not cover it because it is genesis-only, trails the " +
		"allocator, and exists only for the graph prefix",
}

// TestSQLiteStoreDSNOmitsMeasuredOutPragmas is the F1/F2/F3 guard. Every pragma
// on this DSN is multiplied by sqliteStorePerStoreConnections, and these three
// were each measured to be neutral or harmful at that multiplier.
func TestSQLiteStoreDSNOmitsMeasuredOutPragmas(t *testing.T) {
	dsns := []string{
		sqliteStoreDSN("/city/.gc/store/graph/beads.sqlite", false),
		sqliteStoreDSN("/city/.gc/store/graph/beads.sqlite", true),
		sqliteStorePrivateRecoveryDSN("/snapshot/.beads/beads.sqlite"),
	}
	for _, dsn := range dsns {
		for _, pragma := range dsnPragmas(t, dsn) {
			name, _, found := strings.Cut(pragma, "(")
			if !found {
				t.Errorf("DSN %s has malformed _pragma=%s", dsn, pragma)
				continue
			}
			if reason, measuredOut := sqliteMeasuredOutPragmas[name]; measuredOut {
				t.Errorf("DSN %s carries _pragma=%s across %d connections: %s",
					dsn, pragma, sqliteStorePerStoreConnections, reason)
			}
		}
	}
}

// dsnRoundTripPragmas is the exact _pragma list every DSN builder emits, in
// url.Values.Encode order. A rendering that drops or reorders query parameters
// is as broken as one that drops the path, and the path assertions alone would
// not see it.
var dsnRoundTripPragmas = []string{
	"busy_timeout(5000)",
	"foreign_keys(1)",
	sqliteTuningPragma,
}

// assertDSNRoundTrip states DSN correctness as the one property that holds on
// every platform: the DSN parses, the local path survives it byte-for-byte, and
// the query comes back intact.
//
// It deliberately asserts neither the DSN's literal text nor the absence of
// %5C. filepath.ToSlash is a no-op when os.PathSeparator != '\\', so a
// Windows-shaped path keeps its backslashes on Linux and a correct DSN still
// renders them as %5C there; pinning either spelling would fail against a
// correct fix on one platform or the other.
//
// wantPath is normalized through filepath.FromSlash for the same reason the
// literal DSN text is not asserted: the round trip lands in NATIVE spelling,
// because pathutil.LocalPathFromFileURL finishes through filepath.FromSlash.
// A POSIX-literal expectation is therefore correct only off Windows — pinning
// one made four of these five cases fail on the Windows gate against a DSN
// that was right in every case. Deriving the expectation from the input keeps
// the assertion separator-independent in fact, not just in intent, and it is
// true exactly when the DSN is right, because url.URL's escaping and
// pathutil.LocalPathFromFileURL are inverses — including the leading slash
// that carries a drive letter.
func assertDSNRoundTrip(t *testing.T, dsn, wantPath, wantMode string) {
	t.Helper()

	// Native spelling, matching what LocalPathFromFileURL returns. A no-op off
	// Windows, where the separator already is '/'.
	wantPath = filepath.FromSlash(wantPath)

	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", dsn, err)
	}

	// An empty authority is the whole point: "file://C:/x" parses "C:" as a
	// host, which is how a drive letter escapes the path to begin with.
	if parsed.Host != "" {
		t.Errorf("DSN %s parsed to host %q, want empty: the drive letter belongs in the path, never the authority", dsn, parsed.Host)
	}

	got, err := pathutil.LocalPathFromFileURL(parsed)
	if err != nil {
		t.Fatalf("LocalPathFromFileURL(%q): %v", dsn, err)
	}
	if got != wantPath {
		t.Errorf("DSN %s round-tripped to path %q, want %q", dsn, got, wantPath)
	}

	if pragmas := dsnPragmas(t, dsn); !slices.Equal(pragmas, dsnRoundTripPragmas) {
		t.Errorf("DSN %s carries _pragma %v, want %v", dsn, pragmas, dsnRoundTripPragmas)
	}
	if mode := parsed.Query().Get("mode"); mode != wantMode {
		t.Errorf("DSN %s mode = %q, want %q", dsn, mode, wantMode)
	}
}

// TestSQLiteStoreDSNRoundTripsLocalPath is the REQ-003/REQ-004/REQ-005
// regression guard for the Windows file-URL defect. sqliteStoreDSNWithMode
// builds its DSN as url.URL{Scheme: "file", Path: path}, and url.URL.String
// writes "//" ahead of a path that does not start with a slash. A
// drive-lettered path therefore lands in the authority instead of the path and
// the DSN stops parsing altogether, which is why this reproduces as a parse
// error rather than a wrong path.
func TestSQLiteStoreDSNRoundTripsLocalPath(t *testing.T) {
	// A GitHub-hosted Windows runner's temp directory in native spelling: the
	// shape that reaches OpenSQLiteStore through t.TempDir on Windows CI.
	const windowsTempPath = `C:\Users\runneradmin\AppData\Local\Temp\x\beads.sqlite`
	const unixPath = "/tmp/x/beads.sqlite"
	// Every character that means something to a URL parser: a space, a query
	// introducer, a fragment introducer, and a percent sign that must not be
	// read as the start of an escape. All are legal in a POSIX filename, so the
	// DSN has to escape them rather than assume they never occur. This case is
	// what makes a naive fix — concatenating "file:///" onto the path, the way
	// pathutil.FileURLForLocalPath does — fail loudly here instead of silently
	// truncating a real database path at the first "?" in production.
	const metacharPath = "/tmp/T/source ? # % spaces/beads.sqlite"

	cases := []struct {
		name     string
		path     string
		dsn      string
		wantMode string
	}{
		{
			name: "windows drive-lettered path",
			path: windowsTempPath,
			dsn:  sqliteStoreDSN(windowsTempPath, false),
		},
		{
			name: "unix path",
			path: unixPath,
			dsn:  sqliteStoreDSN(unixPath, false),
		},
		{
			name: "path holding url metacharacters",
			path: metacharPath,
			dsn:  sqliteStoreDSN(metacharPath, false),
		},
		{
			name:     "read-only open",
			path:     unixPath,
			dsn:      sqliteStoreDSN(unixPath, true),
			wantMode: "ro",
		},
		{
			name:     "private recovery open",
			path:     metacharPath,
			dsn:      sqliteStorePrivateRecoveryDSN(metacharPath),
			wantMode: "rw",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertDSNRoundTrip(t, tc.dsn, tc.path, tc.wantMode)
		})
	}

	t.Run("unc share path", func(t *testing.T) {
		t.Skip(`known-unverified: a UNC path (\\server\share\beads.sqlite) converts to ` +
			`"//server/share/beads.sqlite", which already starts with a slash, so the builder ` +
			`prepends nothing and url.URL renders "file:////server/share/beads.sqlite" — four ` +
			`slashes, empty authority. The empty-host round trip asserted above is therefore ` +
			`the right contract Go-side; what is unverified is whether that spelling opens a ` +
			`live SMB share, which needs a Windows host with one mounted. Recorded as a gap ` +
			`rather than asserted, so a guess does not become a pinned invariant.`)
	})
}

// TestOpenSQLiteStoreRejectsNonAbsoluteDir pins the boundary guard that keeps a
// relative store directory from being silently relocated to the filesystem
// root.
//
// The leading-slash guard sqliteStoreDSNWithMode needs for the drive letter
// prepends "/" to anything not already slash-prefixed. For an absolute POSIX
// path that is a no-op and for a drive-lettered path it is the fix; for a
// RELATIVE path it is neither — it silently re-anchors ".gc/beads" to
// "/.gc/beads". That is strictly worse than the pre-fix behavior, which failed
// loudly with "invalid uri authority": OpenSQLiteStore's os.Stat would check
// the relative path while SQLite opened the root-anchored one, so the
// read-only and private-recovery gates would adjudicate a different file than
// the one opened, and MkdirAll would create one directory while the database
// landed in another.
//
// No current caller is relative — all six non-test call sites pass absolute
// paths — but OpenSQLiteStore is exported with an unconstrained dir, so the
// guard rejects rather than rewrites. Rejecting is the only safe answer: there
// is no correct base to resolve a relative store path against at this layer.
func TestOpenSQLiteStoreRejectsNonAbsoluteDir(t *testing.T) {
	cases := []struct {
		name string
		dir  string
	}{
		{name: "relative nested path", dir: filepath.Join(".gc", "beads")},
		{name: "bare relative path", dir: "beads"},
		{name: "current directory", dir: "."},
		{name: "empty path", dir: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Any MkdirAll that escapes the guard lands here rather than in the
			// package directory, which is what makes the side-effect assertion
			// below safe to run at all.
			root := t.TempDir()
			t.Chdir(root)

			store, err := OpenSQLiteStore(tc.dir)
			if err == nil {
				if sqliteStore, ok := store.(*SQLiteStore); ok {
					_ = sqliteStore.CloseStore()
				}
				t.Fatalf("OpenSQLiteStore(%q) succeeded, want a rejection: a relative store directory opens a different database than the one the caller named", tc.dir)
			}
			// The path has to be in the message: the caller handed in a
			// relative path by mistake and cannot fix it without seeing it.
			//
			// Matched in its %q rendering, not raw. The guard quotes the path
			// so an empty or space-only one is still visible, and %q escapes a
			// backslash — so on Windows ".gc\beads" reaches the message as
			// ".gc\\beads" and a raw substring match would fail there while
			// passing on Linux. That is the same platform-asymmetric assertion
			// this file was just fixed for; do not reintroduce it.
			if tc.dir != "" && !strings.Contains(err.Error(), fmt.Sprintf("%q", tc.dir)) {
				t.Errorf("OpenSQLiteStore(%q) error = %v, want the rejected path named in the message", tc.dir, err)
			}

			// Rejecting after MkdirAll would still leave the directory behind,
			// so the guard has to run before any side effect.
			entries, readErr := os.ReadDir(root)
			if readErr != nil {
				t.Fatalf("reading %s: %v", root, readErr)
			}
			if len(entries) != 0 {
				t.Errorf("OpenSQLiteStore(%q) created %d entries under the working directory, want none: the guard must reject before MkdirAll", tc.dir, len(entries))
			}
		})
	}
}

func sqlitePragmaValue(t *testing.T, conn *sql.Conn, pragma string) string {
	t.Helper()
	var value string
	if err := conn.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&value); err != nil {
		t.Fatalf("reading PRAGMA %s: %v", pragma, err)
	}
	return value
}

// checkoutAllConns holds every connection the pool will hand out at once, so
// the assertions below cover each one rather than whichever the pool reuses.
// That is the whole reason the pragmas ride the DSN instead of a post-open Exec.
//
// The context is bounded: asking for more connections than the pool will hand
// out blocks forever on database/sql's waiter queue, which surfaces as a
// package-wide test-binary timeout panic instead of a failure here.
func checkoutAllConns(t *testing.T, db *sql.DB, n int) []*sql.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conns := make([]*sql.Conn, 0, n)
	t.Cleanup(func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	})
	for i := 0; i < n; i++ {
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatalf("checking out connection %d of %d: %v", i, n, err)
		}
		conns = append(conns, conn)
	}
	return conns
}

// assertTuningPragmas checks the values every connection must report: the one
// tuned pragma, and the SQLite defaults the measured-out pragmas must leave in
// place. Checking the defaults behaviourally is what catches a pragma restated
// somewhere other than the DSN — the creation schema statements, for instance,
// which run on the store's only write connection.
func assertTuningPragmas(t *testing.T, conns []*sql.Conn) {
	t.Helper()
	for i, conn := range conns {
		for pragma, want := range map[string]string{
			"mmap_size":    "268435456",
			"cache_size":   "-2000", // SQLite's default.
			"temp_store":   "0",     // SQLite's default.
			"synchronous":  "2",     // FULL.
			"busy_timeout": "5000",
			"foreign_keys": "1",
		} {
			if got := sqlitePragmaValue(t, conn, pragma); got != want {
				t.Errorf("connection %d: PRAGMA %s = %s, want %s", i, pragma, got, want)
			}
		}
	}
}

func TestSQLiteStoreConnectionsApplyTuningPragmas(t *testing.T) {
	dir := t.TempDir()
	opened, err := OpenSQLiteStore(dir, WithSQLiteStoreIDPrefix(sqliteGraphPrefix))
	if err != nil {
		t.Fatalf("OpenSQLiteStore: %v", err)
	}
	store := opened.(*SQLiteStore)
	t.Cleanup(func() { _ = store.CloseStore() })

	// The write connection is created by applySchema on a brand-new database,
	// so this also guards the creation statements against drifting out of
	// lockstep with the DSN and silently overriding it for the process's only
	// writer.
	t.Run("write connection", func(t *testing.T) {
		assertTuningPragmas(t, checkoutAllConns(t, store.db, 1))
	})
	t.Run("read pool", func(t *testing.T) {
		assertTuningPragmas(t, checkoutAllConns(t, store.readDB, sqliteReadPoolSize))
	})
}

func TestSQLiteStoreReadOnlyConnectionsApplyTuningPragmas(t *testing.T) {
	dir := t.TempDir()
	opened, err := OpenSQLiteStore(dir, WithSQLiteStoreIDPrefix(sqliteGraphPrefix))
	if err != nil {
		t.Fatalf("OpenSQLiteStore writer: %v", err)
	}
	if err := opened.(*SQLiteStore).CloseStore(); err != nil {
		t.Fatalf("CloseStore writer: %v", err)
	}

	opened, err = OpenSQLiteStore(dir, WithSQLiteStoreReadOnly(), WithSQLiteStoreIDPrefix(sqliteGraphPrefix))
	if err != nil {
		t.Fatalf("OpenSQLiteStore read-only: %v", err)
	}
	store := opened.(*SQLiteStore)
	t.Cleanup(func() { _ = store.CloseStore() })

	if _, err := store.List(ListQuery{AllowScan: true, IncludeClosed: true}); err != nil {
		t.Fatalf("List through tuned read-only store: %v", err)
	}

	// Checking out the whole pool has to come last: it leaves no connection
	// for List.
	assertTuningPragmas(t, checkoutAllConns(t, store.readDB, sqliteReadPoolSize))
}

// TestSQLiteStoreKeepsFullSynchronousUntilTheSequenceFloorLeads states the
// invariant behind the synchronous entry in sqliteMeasuredOutPragmas rather
// than restating the pragma value.
//
// recoverSequence rebuilds the allocator at open from MAX(numeric suffix) over
// durable rows. Under synchronous=FULL a commit is fsynced before Create
// returns, so an ID the caller has already seen is durable and the allocator
// cannot regress below it. Under NORMAL the whole WAL tail since the last
// checkpoint can be discarded by a host crash, the allocator regresses, and the
// next mints reissue IDs that already escaped. The test asserts the premise
// (the persisted floor trails the ids handed out) and the consequence (the
// write connection is at FULL) together, so it stops guarding as soon as the
// premise stops being true instead of silently pinning a stale conclusion.
func TestSQLiteStoreKeepsFullSynchronousUntilTheSequenceFloorLeads(t *testing.T) {
	dir := t.TempDir()
	opened, err := OpenSQLiteStore(dir, WithSQLiteStoreIDPrefix(sqliteGraphPrefix))
	if err != nil {
		t.Fatalf("OpenSQLiteStore: %v", err)
	}
	store := opened.(*SQLiteStore)
	t.Cleanup(func() { _ = store.CloseStore() })

	escaped := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		created, err := store.Create(Bead{Title: fmt.Sprintf("escaped %d", i), Status: "open", Type: "task"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		escaped = append(escaped, created.ID)
	}
	floor, err := store.SequenceFloor()
	if err != nil {
		t.Fatalf("SequenceFloor: %v", err)
	}
	minted := store.seq.Load()
	if minted == 0 {
		t.Fatal("no ids were minted, so the guard below would be vacuous")
	}
	if floor >= minted {
		t.Skipf("sequence floor %d now leads the allocator at %d after minting %v; re-evaluate synchronous=NORMAL", floor, minted, escaped)
	}

	conn := checkoutAllConns(t, store.db, 1)[0]
	if got := sqlitePragmaValue(t, conn, "synchronous"); got != "2" {
		t.Errorf("write connection PRAGMA synchronous = %s, want 2 (FULL): the allocator recovers from durable rows but the persisted floor (%d) trails the %d ids already handed out (%v)", got, floor, minted, escaped)
	}
}
