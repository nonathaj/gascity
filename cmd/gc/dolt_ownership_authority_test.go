package main

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestOwnershipIsAuthoritativeNotPidEquality is item 7 of the test plan in
// engdocs/contributors/windows-pid-space.md, and the gate for Phase 3.
//
// managedDoltRuntimeProcessOwned had a shortcut: if the process holding the dolt port had
// the same pid as the one remembered in dolt-state.json, it returned true WITHOUT consulting
// ownership inspection. Section 8a measured that directly — the function returned true while
// `owned` was false, passing purely through pid equality.
//
// Two problems follow, and both are the same short-circuit:
//
//   - It makes the remembered pid the whole answer. Phase 3 wants to derive the pid from the
//     port instead, which would turn the comparison into holderPID == holderPID —
//     tautologically true, accepting ANY process on the port. That is the class of vacuous
//     check this thread has been removing, so the shortcut has to go first.
//   - A recycled pid that coincides with the port holder is accepted as managed dolt without
//     anything ever verifying it IS dolt. Vanishingly unlikely, and still wrong.
//
// This test holds the port in a process that is definitely NOT dolt — the test binary — and
// records its pid as the managed one. Ownership must reject it. Before the fix this returns
// true on the pid-equality branch.
func TestOwnershipIsAuthoritativeNotPidEquality(t *testing.T) {
	// Skipped, not deleted, and not softened to match today's behavior.
	//
	// It has been RUN and it fails against the shortcut, which is the evidence that matters:
	// managedDoltRuntimeProcessOwned accepts this test binary as managed dolt. The mechanical
	// prerequisite is already in — processArgs reads argv natively on Windows — so what blocks
	// the fix is fixtures, not capability. At least six tests fabricate running dolt as
	// `PID: os.Getpid()` with the test binary holding the port, and they pass only because of
	// the shortcut this test condemns: TestCurrentDoltPortPrefersRuntimeState,
	// TestEnsureBeadsProviderPublishesManagedDoltRuntimeStateFromProviderState,
	// TestPublishManagedDoltRuntimeStateIfOwnedPublishesForInheritedBdRigUnderFileCity, and the
	// three TestResolvedRuntimeCityDoltTarget* cases.
	//
	// Un-skip as the last step of gw-dbm Phase 3, once those fixtures use a stand-in that both
	// holds the port and carries dolt-like argv.
	t.Skip("gw-dbm Phase 3: encodes the target contract; fails today against the pid-equality " +
		"shortcut, whose removal needs six fixtures migrated off `PID: os.Getpid()` first")

	// listenOnRandomPort rather than a fresh net.Listen: the shared helper already carries
	// this package's listener accounting in the resource census, and net_listen's baseline is
	// pinned by TestBootstrapPolicyOwnsNetListenDebtAndExactMediumOwners.
	listener := listenOnRandomPort(t)
	t.Cleanup(func() { _ = listener.Close() })
	port := listener.Addr().(*net.TCPAddr).Port

	dir := t.TempDir()
	layout := managedDoltRuntimeLayout{
		PackStateDir: dir,
		DataDir:      filepath.Join(dir, "data"),
		StateFile:    filepath.Join(dir, "dolt-state.json"),
		ConfigFile:   filepath.Join(dir, "dolt-config.yaml"),
	}
	if err := os.MkdirAll(layout.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// data_dir matches, so Layer 1 cannot be what rejects: the verdict has to come from
	// ownership inspection, which is the point of the test.
	state := doltRuntimeState{
		Running:   true,
		PID:       os.Getpid(),
		Port:      port,
		DataDir:   layout.DataDir,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeDoltRuntimeStateFile(layout.StateFile, state); err != nil {
		t.Fatalf("write dolt runtime state: %v", err)
	}

	// Anti-vacuity guard. If port attribution cannot name this process, the pid-equality
	// branch is unreachable and a pass would prove nothing — the function would reach
	// `return owned` and answer false for the wrong reason.
	if holder := findPortHolderPID(strconv.Itoa(port)); holder != os.Getpid() {
		t.Skipf("port %d attributed to pid %d, not this process (%d); the pid-equality branch "+
			"this test exists to close is not reachable here", port, holder, os.Getpid())
	}

	if managedDoltRuntimeProcessOwned(state, layout) {
		t.Fatalf("managedDoltRuntimeProcessOwned accepted pid %d as managed dolt. It is this "+
			"test binary holding the port, not dolt: its argv names no --config of ours and its "+
			"cwd is not the data dir, so ownership inspection says NOT ours. Accepting it means "+
			"pid equality is overriding ownership, which makes the check vacuous once the pid is "+
			"derived from the port (windows-pid-space.md 8a MEASURE 2)", state.PID)
	}
}

// TestOwnershipAcceptsAFaithfulDoltStandIn is the positive half, and the feasibility proof for
// migrating fixtures off `PID: os.Getpid()`.
//
// A stand-in that holds the dolt port AND carries `--config <ours>` in its argv must be judged
// OURS by ownership inspection alone — with no help from pid equality. If this could not pass,
// there would be no way to write a faithful fixture and the pid-equality shortcut could never
// be removed.
func TestOwnershipAcceptsAFaithfulDoltStandIn(t *testing.T) {
	dir := t.TempDir()
	layout := managedDoltRuntimeLayout{
		PackStateDir: dir,
		DataDir:      filepath.Join(dir, "data"),
		StateFile:    filepath.Join(dir, "dolt-state.json"),
		ConfigFile:   filepath.Join(dir, "dolt-config.yaml"),
	}
	if err := os.MkdirAll(layout.DataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	standIn := startDoltStandInForConfig(t, layout.ConfigFile)
	state := doltRuntimeState{
		Running:   true,
		PID:       standIn.PID,
		Port:      standIn.Port,
		DataDir:   layout.DataDir,
		StartedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeDoltRuntimeStateFile(layout.StateFile, state); err != nil {
		t.Fatalf("write dolt runtime state: %v", err)
	}

	if !managedDoltRuntimeProcessOwned(state, layout) {
		args, _ := processArgs(standIn.PID)
		t.Fatalf("ownership rejected a faithful stand-in (pid %d, port %d).\n"+
			"Its argv is %q and our config is %q. If argv cannot be read, or the --config match "+
			"fails, then no fixture can represent managed dolt without the pid-equality "+
			"shortcut, and gw-dbm Phase 3 is blocked on mechanism rather than fixtures.",
			standIn.PID, standIn.Port, args, layout.ConfigFile)
	}
}
