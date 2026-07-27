package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/execshim"
	"github.com/gastownhall/gascity/internal/pidutil"
	"github.com/gastownhall/gascity/internal/testutil"
)

// TestMixedOriginStopTerminatesHelperRecordedServer is plan item #6 from
// engdocs/contributors/windows-pid-space.md, and the integration test for this whole
// thread: it exercises a server recorded by ONE tier and stopped by the OTHER.
//
// Go spawns the process (so its pid is native by construction, exactly as
// `gc dolt-state start-managed` would record it), and the pack script's stop op runs
// with GC_BIN unset so sh must do the work itself. Reaching a dead process requires all
// three fixes to compose:
//
//   - gw-dbm  — the persisted pid is in the space Go and sh now agree on, so
//     find_dolt_pid's pid_alive recognizes it instead of discarding the pid file.
//   - gw-1ay  — verify_our_server accepts on the state-file fallback instead of
//     hard-failing where ps cannot report args, so owned=true and the kill is attempted
//     at all.
//   - gw-591  — pid_kill's taskkill fallback can actually terminate a native pid, which
//     MSYS kill cannot.
//
// Any one of them regressing leaves the process alive, which is precisely the
// user-visible bug: `stop` reporting success while the server keeps running.
func TestMixedOriginStopTerminatesHelperRecordedServer(t *testing.T) {
	skipSlowCmdGCTest(t, "spawns a real process and runs the script stop op; run make test-cmd-gc-process for full coverage")

	cityPath := t.TempDir()
	packStateDir := filepath.Join(cityPath, ".gc", "runtime", "packs", "dolt")
	dataDir := filepath.Join(cityPath, ".beads", "dolt")
	for _, dir := range []string{packStateDir, dataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeCityToml(t, cityPath, "[workspace]\nname = \"mixed-origin-city\"\n")
	materializeBuiltinPacksForTest(t, cityPath)
	script := gcBeadsBdScriptPath(cityPath)

	// Go spawns the stand-in server. cmd.Process.Pid is a native pid by construction —
	// the same thing the Go start-managed helper records.
	// 600s, far beyond any path through this test: a shorter stand-in could expire on
	// its own and make the assertion pass for the wrong reason. The first run of this
	// test took 127s against a sleep 120, so that was not hypothetical.
	victim := exec.Command(execshim.ShPath(), "-c", "exec sleep 600")
	if err := victim.Start(); err != nil {
		t.Fatalf("start stand-in server: %v", err)
	}
	nativePID := victim.Process.Pid
	t.Cleanup(func() {
		_ = pidutil.KillTree(nativePID)
		_ = victim.Process.Kill()
		_, _ = victim.Process.Wait()
	})
	go func() { _ = victim.Wait() }()

	if !pidutil.Alive(nativePID) {
		t.Fatalf("stand-in server pid %d not live at setup", nativePID)
	}

	port := reserveRandomTCPPort(t)
	statePath := filepath.Join(packStateDir, "dolt-state.json")
	pidPath := filepath.Join(packStateDir, "dolt.pid")

	// Record it the way the Go helper does: native pid in the pid file, and a state file
	// whose data_dir matches so ownership can be established without ps args.
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(nativePID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stateJSON := fmt.Sprintf(`{"running":true,"pid":%d,"port":%d,"data_dir":%q,"started_at":%q}`,
		nativePID, port, filepath.ToSlash(dataDir), time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(statePath, []byte(stateJSON), 0o644); err != nil {
		t.Fatal(err)
	}

	binDir := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	installFakeToolOnPath(t, binDir, "dolt", "#!/bin/sh\nexit 0\n")
	installFakeToolOnPath(t, binDir, "bd", "#!/bin/sh\nexit 0\n")

	cmd := execshim.Command(script, "stop")
	cmd.Env = prependPathDir(sanitizedBaseEnv(
		"GC_CITY_PATH="+cityPath,
		"GC_DOLT_HOST=127.0.0.1",
		"GC_DOLT_PORT="+strconv.Itoa(port),
		"GC_PACK_STATE_DIR="+packStateDir,
		"GC_DOLT_DATA_DIR="+dataDir,
		"GC_DOLT_LOG_FILE="+filepath.Join(packStateDir, "dolt.log"),
		"GC_DOLT_STATE_FILE="+statePath,
		"GC_DOLT_PID_FILE="+pidPath,
		"GC_DOLT_LOCK_FILE="+filepath.Join(packStateDir, "dolt.lock"),
		"GC_DOLT_CONFIG_FILE="+filepath.Join(packStateDir, "dolt-config.yaml"),
		"PATH="+strings.Join([]string{binDir, os.Getenv("PATH")}, string(os.PathListSeparator)),
	), binDir)
	out, runErr := cmd.CombinedOutput()

	if testutil.WaitUntil(45*time.Second, func() bool { return !pidutil.Alive(nativePID) }) {
		return
	}
	t.Fatalf("the script's stop op left pid %d running (stop err %v). A stop that reports "+
		"success without stopping is the gw-1ay symptom; a stop that cannot recognize or "+
		"signal the pid is gw-dbm/gw-591.\nstop output:\n%s", nativePID, runErr, out)
}
