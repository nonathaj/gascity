package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Windows supervisor hosting contract (gw-x1k, D4).
//
// Every test here stubs schtasksRun, so the suite never creates, queries or
// deletes a real scheduled task. That is deliberate: a test that registered a
// logon-triggered task on the developer's machine would outlive the test run
// and start a supervisor at their next login.

func stubSchtasks(t *testing.T, handler func(args ...string) (string, error)) *[][]string {
	t.Helper()
	var calls [][]string
	original := schtasksRun
	schtasksRun = func(args ...string) (string, error) {
		calls = append(calls, args)
		return handler(args...)
	}
	t.Cleanup(func() { schtasksRun = original })
	return &calls
}

func testServiceData(t *testing.T) *supervisorServiceData {
	t.Helper()
	return &supervisorServiceData{
		GCPath:            filepath.Join(t.TempDir(), "gc.exe"),
		LogPath:           filepath.Join(t.TempDir(), "supervisor.log"),
		GCHome:            os.Getenv("GC_HOME"),
		Path:              `C:\Windows\system32;C:\Program Files\Git\usr\bin`,
		PortInUseExitCode: supervisorExitCodePortInUse,
	}
}

// TestSupervisorTaskNamesAreDistinctAndHomeScoped pins that the two tasks never
// collide with each other, and that isolated homes never collide with the real
// supervisor's task — the property that lets one machine host several.
func TestSupervisorTaskNamesAreDistinctAndHomeScoped(t *testing.T) {
	setTestHome(t, t.TempDir())

	t.Setenv("GC_HOME", t.TempDir())
	isolatedTask, isolatedKeepalive := supervisorTaskName(), supervisorKeepaliveTaskName()
	if isolatedTask == isolatedKeepalive {
		t.Fatalf("supervisor and keepalive task names are identical (%q); one would overwrite the other", isolatedTask)
	}

	t.Setenv("GC_HOME", t.TempDir())
	otherTask := supervisorTaskName()
	if otherTask == isolatedTask {
		t.Fatalf("two different GC_HOMEs produced the same task name %q; installing one would "+
			"silently replace the other's supervisor", otherTask)
	}
	for _, name := range []string{isolatedTask, isolatedKeepalive, otherTask} {
		if !supervisorTaskIsGCOwned(name) {
			t.Fatalf("supervisorTaskIsGCOwned(%q) = false; the sweep would not recognize gc's own task", name)
		}
	}
}

// TestSupervisorSweepNeverTargetsTheUnsuffixedTask is the safety property of the
// stale sweep: the bare "GasCity Supervisor" is the user's real supervisor and
// must never be swept, exactly as the systemd sweep protects the unsuffixed
// gascity-supervisor.service.
func TestSupervisorSweepNeverTargetsTheUnsuffixedTask(t *testing.T) {
	if supervisorTaskIsIsolated(supervisorTaskPrefix) {
		t.Fatal("the unsuffixed task was judged isolated; the sweep would delete the user's real supervisor task")
	}
	if !supervisorTaskIsIsolated(supervisorTaskPrefix + " (city-abc12345)") {
		t.Fatal("a suffixed task was not judged isolated; stale isolated tasks would accumulate forever")
	}
	for _, foreign := range []string{
		"GasCity Supervisor Helper by someone else",
		"Some Other Task (x)",
		"GasCityRelated (y)",
	} {
		if supervisorTaskIsGCOwned(foreign) && supervisorTaskIsIsolated(foreign) {
			t.Fatalf("foreign task %q was judged a sweepable gc task", foreign)
		}
	}
}

// TestSupervisorTaskWrapperRoundTripsBinaryAndHome pins the two values the
// drift guard and the stale sweep depend on surviving render -> parse.
func TestSupervisorTaskWrapperRoundTripsBinaryAndHome(t *testing.T) {
	setTestHome(t, t.TempDir())
	home := t.TempDir()
	t.Setenv("GC_HOME", home)

	data := testServiceData(t)
	data.GCHome = home
	wrapper := renderSupervisorTaskWrapper(data, "supervisor", "run")

	if got := supervisorTaskWrapperBinary(wrapper); got != data.GCPath {
		t.Fatalf("supervisorTaskWrapperBinary = %q, want %q; the drift guard cannot detect a "+
			"binary change it cannot read back", got, data.GCPath)
	}
	if got := supervisorTaskWrapperGCHome(wrapper); got != home {
		t.Fatalf("supervisorTaskWrapperGCHome = %q, want %q; the stale sweep decides whether to "+
			"DELETE a task from this value", got, home)
	}
}

// TestSupervisorTaskWrapperBakesEnvironmentAndEscapesPercent covers the reason a
// wrapper exists at all: Task Scheduler carries no environment, so anything the
// unit declares must land here instead.
func TestSupervisorTaskWrapperBakesEnvironmentAndEscapesPercent(t *testing.T) {
	setTestHome(t, t.TempDir())
	home := t.TempDir()
	t.Setenv("GC_HOME", home)

	data := testServiceData(t)
	data.GCHome = home
	data.Path = `C:\Windows\system32;%SystemRoot%\tools`
	data.ExtraEnv = []supervisorServiceEnvVar{{Name: "ANTHROPIC_API_KEY", Value: "secret-value"}}

	wrapper := renderSupervisorTaskWrapper(data, "supervisor", "run")

	for _, want := range []string{
		`set "GC_HOME=` + home + `"`,
		`set "` + supervisorPreserveSessionsOnSignalEnv + `=1"`,
		`set "ANTHROPIC_API_KEY=secret-value"`,
	} {
		if !strings.Contains(wrapper, want) {
			t.Fatalf("wrapper missing %q; the task would run without it\n%s", want, wrapper)
		}
	}
	// A single % would be consumed by batch expansion at RUN time, so the
	// installed PATH would silently differ from the one gc computed.
	if !strings.Contains(wrapper, `%%SystemRoot%%`) {
		t.Fatalf("percent signs were not doubled; batch would re-expand them at run time\n%s", wrapper)
	}
	if !strings.Contains(wrapper, "supervisor run") {
		t.Fatalf("wrapper does not invoke 'supervisor run'\n%s", wrapper)
	}
}

// TestSupervisorInstallRegistersBothTasksWithoutSchedulerRestart is the crux of
// D4: the supervisor task must NOT carry a Task Scheduler restart, because the
// scheduler cannot exempt the port-collision exit code and would crash-loop it.
// Restart belongs to the keepalive task instead.
func TestSupervisorInstallRegistersBothTasksWithoutSchedulerRestart(t *testing.T) {
	setTestHome(t, t.TempDir())
	home := t.TempDir()
	t.Setenv("GC_HOME", home)

	calls := stubSchtasks(t, func(...string) (string, error) { return "SUCCESS", nil })
	data := testServiceData(t)
	data.GCHome = home

	var stdout, stderr bytes.Buffer
	if code := installSupervisorSchtasks(data, &stdout, &stderr); code != 0 {
		t.Fatalf("install = %d, want 0\nstderr: %s", code, stderr.String())
	}

	var createdSupervisor, createdKeepalive []string
	for _, call := range *calls {
		joined := strings.Join(call, " ")
		if !strings.Contains(joined, "/Create") {
			continue
		}
		switch {
		case strings.Contains(joined, supervisorKeepaliveTaskName()):
			createdKeepalive = call
		case strings.Contains(joined, supervisorTaskName()):
			createdSupervisor = call
		}
	}
	if createdSupervisor == nil {
		t.Fatalf("no /Create for the supervisor task; calls: %v", *calls)
	}
	if createdKeepalive == nil {
		t.Fatalf("no /Create for the keepalive task; without it nothing ever restarts the supervisor; calls: %v", *calls)
	}

	// Compare exact argv tokens, not a substring of the joined command line: the
	// arguments embed temp paths, and t.TempDir() bakes the test's own name into
	// them, so a substring search for "restart" matches this test's name and
	// reports a failure that does not exist.
	for _, forbidden := range []string{"/RI", "/MO", "/DU"} {
		for _, arg := range createdSupervisor {
			if strings.EqualFold(arg, forbidden) {
				t.Fatalf("supervisor task registered with %s; a repeating/restarting supervisor task "+
					"would relaunch a port-collision exit %d that Task Scheduler cannot exempt, "+
					"crash-looping against a port another supervisor owns (D4). Restart belongs to "+
					"the keepalive task, which asks gc first.",
					forbidden, supervisorExitCodePortInUse)
			}
		}
	}
	if !argvContains(createdSupervisor, "ONLOGON") {
		t.Fatalf("supervisor task is not logon-triggered: %v", createdSupervisor)
	}
	if !argvContains(createdKeepalive, "MINUTE") {
		t.Fatalf("keepalive task is not a repeating trigger: %v", createdKeepalive)
	}

	// Both wrappers must exist on disk, and the keepalive must invoke ensure —
	// not run — or the keepalive would start a SECOND supervisor every 5 minutes.
	keepaliveWrapper, err := os.ReadFile(supervisorKeepaliveWrapperPath())
	if err != nil {
		t.Fatalf("read keepalive wrapper: %v", err)
	}
	if !strings.Contains(string(keepaliveWrapper), "supervisor ensure") {
		t.Fatalf("keepalive wrapper does not invoke 'supervisor ensure':\n%s", keepaliveWrapper)
	}
	if strings.Contains(string(keepaliveWrapper), "supervisor run") {
		t.Fatalf("keepalive wrapper invokes 'supervisor run'; every trigger would start another "+
			"supervisor instead of checking whether one is needed:\n%s", keepaliveWrapper)
	}
}

// TestSupervisorInstallRefusesBinaryDrift is the Task Scheduler analog of the
// systemd ExecStart guard.
func TestSupervisorInstallRefusesBinaryDrift(t *testing.T) {
	setTestHome(t, t.TempDir())
	home := t.TempDir()
	t.Setenv("GC_HOME", home)
	stubSchtasks(t, func(...string) (string, error) { return "SUCCESS", nil })

	first := testServiceData(t)
	first.GCHome = home
	var out, errOut bytes.Buffer
	if code := installSupervisorSchtasks(first, &out, &errOut); code != 0 {
		t.Fatalf("first install = %d, want 0\nstderr: %s", code, errOut.String())
	}

	second := testServiceData(t)
	second.GCHome = home
	second.GCPath = filepath.Join(t.TempDir(), "other-gc.exe")

	out.Reset()
	errOut.Reset()
	supervisorInstallForce = false
	if code := installSupervisorSchtasks(second, &out, &errOut); code == 0 {
		t.Fatal("install from a different binary succeeded without --force; a stray build would " +
			"silently take over the machine's supervisor task")
	}
	if !strings.Contains(errOut.String(), "--force") {
		t.Fatalf("refusal does not mention the --force override: %s", errOut.String())
	}

	// --force is the documented escape hatch and must work.
	supervisorInstallForce = true
	t.Cleanup(func() { supervisorInstallForce = false })
	out.Reset()
	errOut.Reset()
	if code := installSupervisorSchtasks(second, &out, &errOut); code != 0 {
		t.Fatalf("install --force = %d, want 0\nstderr: %s", code, errOut.String())
	}
}

// TestSupervisorSweepDeletesOnlyTasksWhoseHomeIsGone pins that the sweep is
// driven by the GC_HOME actually recorded in each task's wrapper, not by the
// name alone.
func TestSupervisorSweepDeletesOnlyTasksWhoseHomeIsGone(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("GC_HOME", t.TempDir())

	liveHome := t.TempDir()
	deadHome := filepath.Join(t.TempDir(), "removed-city")

	wrapperDir := t.TempDir()
	liveWrapper := filepath.Join(wrapperDir, "live.cmd")
	deadWrapper := filepath.Join(wrapperDir, "dead.cmd")
	writeWrapper := func(path, home string) {
		data := testServiceData(t)
		data.GCHome = home
		if err := os.WriteFile(path, []byte(renderSupervisorTaskWrapper(data, "supervisor", "run")), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeWrapper(liveWrapper, liveHome)
	writeWrapper(deadWrapper, deadHome)

	const liveTask = `GasCity Supervisor (live-11111111)`
	const deadTask = `GasCity Supervisor (dead-22222222)`

	var deleted []string
	stubSchtasks(t, func(args ...string) (string, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "/Query") && strings.Contains(joined, "CSV"):
			return "\"" + liveTask + "\",\"N/A\",\"Ready\"\n\"" + deadTask + "\",\"N/A\",\"Ready\"\n", nil
		case strings.Contains(joined, "/Query") && strings.Contains(joined, liveTask):
			return "Task To Run:   " + liveWrapper + "\n", nil
		case strings.Contains(joined, "/Query") && strings.Contains(joined, deadTask):
			return "Task To Run:   " + deadWrapper + "\n", nil
		case strings.Contains(joined, "/Delete"):
			for i, a := range args {
				if a == "/TN" && i+1 < len(args) {
					deleted = append(deleted, args[i+1])
				}
			}
			return "SUCCESS", nil
		}
		return "", nil
	})

	var stderr bytes.Buffer
	sweepStaleIsolatedSupervisorSchtasks(&stderr)

	if len(deleted) != 1 || deleted[0] != deadTask {
		t.Fatalf("deleted = %v, want exactly [%q]. Deleting a task whose GC_HOME still exists "+
			"would remove a live supervisor's registration", deleted, deadTask)
	}
	if !strings.Contains(stderr.String(), deadHome) {
		t.Fatalf("sweep did not report which GC_HOME was gone: %s", stderr.String())
	}
}

// TestParseSchtasksTaskNamesHandlesRepeatedHeaders covers the format quirk that
// would otherwise feed a literal "TaskName" row into the sweep.
func TestParseSchtasksTaskNamesHandlesRepeatedHeaders(t *testing.T) {
	got := parseSchtasksTaskNames(strings.Join([]string{
		`"TaskName","Next Run Time","Status"`,
		`"\GasCity Supervisor (a-1)","N/A","Ready"`,
		``,
		`"TaskName","Next Run Time","Status"`,
		`"\Some Other Task","12:00:00","Ready"`,
	}, "\n"))

	want := []string{`\GasCity Supervisor (a-1)`, `\Some Other Task`}
	if len(got) != len(want) {
		t.Fatalf("parseSchtasksTaskNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseSchtasksTaskNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestSupervisorUninstallToleratesMissingTasks pins that uninstall is runnable
// on a machine where install never ran — a common state when an operator is
// cleaning up after a failed install.
func TestSupervisorUninstallToleratesMissingTasks(t *testing.T) {
	setTestHome(t, t.TempDir())
	t.Setenv("GC_HOME", t.TempDir())

	stubSchtasks(t, func(...string) (string, error) {
		return "ERROR: The system cannot find the file specified.", errFakeSchtasksMissing
	})

	var stdout, stderr bytes.Buffer
	if code := uninstallSupervisorSchtasks(testServiceData(t), &stdout, &stderr); code != 0 {
		t.Fatalf("uninstall = %d, want 0 when no task was ever installed\nstderr: %s", code, stderr.String())
	}
}

// errFakeSchtasksMissing stands in for the non-zero exit schtasks returns when
// asked to delete a task that does not exist.
var errFakeSchtasksMissing = errors.New("exit status 1")

// argvContains reports whether argv holds an exact token, case-insensitively.
// Used instead of searching the joined command line because the arguments embed
// temp paths that contain the test's own name.
func argvContains(argv []string, token string) bool {
	for _, arg := range argv {
		if strings.EqualFold(arg, token) {
			return true
		}
	}
	return false
}
