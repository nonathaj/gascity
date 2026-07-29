package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gastownhall/gascity/internal/supervisor"
)

// Windows supervisor hosting via Task Scheduler (gw-x1k, D4 of
// engdocs/design/windows-systemd-parity.md).
//
// Windows has no systemd, and its two service hosts each fail this job in a
// different way. A real Windows Service must implement the Service Control
// Manager protocol and runs in a non-interactive session, which is wrong for a
// supervisor that spawns terminal multiplexers on the user's desktop. Task
// Scheduler runs an ordinary process in the user's own session, which is right
// -- so it is what the parity design targets.
//
// The load-bearing difference from systemd: Task Scheduler cannot restart on a
// filtered exit code. It offers restart-on-failure with no way to say "except
// exit 3", so a restart-configured task would crash-loop a port collision
// forever. Restart therefore stays owned by gc: this file registers the task
// WITHOUT scheduler restarts, plus a second repeating task that runs
// `gc supervisor ensure` (see cmd_supervisor_ensure.go), which makes that
// judgment where the exit code is actually known.

const (
	// supervisorTaskPrefix is the Task Scheduler analog of
	// supervisorSystemdUnitPrefix. Isolated GC_HOMEs append the same
	// supervisorServiceSuffix() the unit and launchd label already use, so one
	// machine can host several supervisors without their tasks colliding.
	supervisorTaskPrefix = "GasCity Supervisor"

	// supervisorKeepaliveTaskSuffix distinguishes the repeating keepalive task
	// from the logon-triggered supervisor task. Both carry the GC_HOME suffix.
	supervisorKeepaliveTaskSuffix = "Keepalive"

	// supervisorKeepaliveMinutes is how often the keepalive task runs. Five
	// minutes matches RestartSec's intent rather than its latency: this covers
	// the tail case of a supervisor that died outright, and a shorter period
	// would spend more waking up to find nothing wrong than it would save.
	supervisorKeepaliveMinutes = 5

	// supervisorTaskWrapperName is the batch file the task actually invokes.
	//
	// Task Scheduler has no environment support -- there is no XML element
	// corresponding to systemd's Environment= lines -- so the environment has
	// to be baked into something the action can run. Rendering a wrapper keeps
	// that explicit and inspectable on disk, exactly like the unit file it
	// mirrors, rather than smuggling `cmd /c set X=... && ...` into the action
	// string where nothing can read it back.
	supervisorTaskWrapperName = "supervisor-task.cmd"

	// supervisorKeepaliveWrapperName is the keepalive task's wrapper. It shares
	// the supervisor wrapper's baked environment but runs `supervisor ensure`.
	supervisorKeepaliveWrapperName = "supervisor-keepalive.cmd"

	// supervisorTaskWrapperBinaryMarker tags the line naming the gc binary so
	// the drift guard can recover it without parsing batch syntax.
	supervisorTaskWrapperBinaryMarker = "@rem gc-binary: "

	// supervisorTaskWrapperHomeMarker tags the line naming GC_HOME so the stale
	// sweep can recover it the same way.
	supervisorTaskWrapperHomeMarker = "@rem gc-home: "
)

// supervisorTaskName returns the Task Scheduler task name for this GC_HOME.
func supervisorTaskName() string {
	if suffix := supervisorServiceSuffix(); suffix != "" {
		return fmt.Sprintf("%s (%s)", supervisorTaskPrefix, suffix)
	}
	return supervisorTaskPrefix
}

// supervisorKeepaliveTaskName returns the repeating keepalive task's name.
func supervisorKeepaliveTaskName() string {
	if suffix := supervisorServiceSuffix(); suffix != "" {
		return fmt.Sprintf("%s %s (%s)", supervisorTaskPrefix, supervisorKeepaliveTaskSuffix, suffix)
	}
	return supervisorTaskPrefix + " " + supervisorKeepaliveTaskSuffix
}

// supervisorTaskWrapperPath returns the on-disk path of the supervisor task's
// wrapper batch file.
func supervisorTaskWrapperPath() string {
	return filepath.Join(supervisor.RuntimeDir(), supervisorTaskWrapperName)
}

// supervisorKeepaliveWrapperPath returns the keepalive task's wrapper path.
func supervisorKeepaliveWrapperPath() string {
	return filepath.Join(supervisor.RuntimeDir(), supervisorKeepaliveWrapperName)
}

// batchEscapeValue makes s safe to appear inside a `set "NAME=<s>"` line.
//
// Batch expands %VAR% during parsing, so a literal percent must be doubled or a
// PATH containing something like %SystemRoot% would be re-expanded at task run
// time -- silently producing a different environment than the one installed.
// Quotes are stripped rather than escaped: `set "NAME=value"` has no escape for
// an embedded quote, and no environment value gc bakes here legitimately
// contains one.
func batchEscapeValue(s string) string {
	s = strings.ReplaceAll(s, "%", "%%")
	s = strings.ReplaceAll(s, `"`, "")
	return strings.ReplaceAll(s, "\r\n", " ")
}

// renderSupervisorTaskWrapper renders the batch wrapper that a Task Scheduler
// action invokes, carrying the same environment the systemd unit declares.
//
// gcArgs are the gc subcommand arguments ("supervisor", "run" or "supervisor",
// "ensure"), so the supervisor and keepalive tasks share one renderer and
// therefore one environment by construction -- a keepalive that respawned the
// supervisor under a different environment than the install baked would be a
// very hard bug to see.
func renderSupervisorTaskWrapper(data *supervisorServiceData, gcArgs ...string) string {
	var b strings.Builder
	b.WriteString("@echo off\r\n")
	b.WriteString("@rem Generated by 'gc supervisor install' -- do not edit.\r\n")
	b.WriteString("@rem Task Scheduler cannot carry environment variables, so they are baked here.\r\n")
	// Machine-readable markers for the drift guard and the stale sweep. They are
	// comments so the file stays a valid batch script.
	b.WriteString(supervisorTaskWrapperBinaryMarker + data.GCPath + "\r\n")
	b.WriteString(supervisorTaskWrapperHomeMarker + data.GCHome + "\r\n")

	fmt.Fprintf(&b, "set \"GC_HOME=%s\"\r\n", batchEscapeValue(data.GCHome))
	fmt.Fprintf(&b, "set \"PATH=%s\"\r\n", batchEscapeValue(data.Path))
	fmt.Fprintf(&b, "set \"%s=1\"\r\n", supervisorPreserveSessionsOnSignalEnv)
	for _, env := range data.ExtraEnv {
		fmt.Fprintf(&b, "set \"%s=%s\"\r\n", env.Name, batchEscapeValue(env.Value))
	}

	// Append rather than truncate, matching the unit's StandardOutput=append:.
	fmt.Fprintf(&b, "\"%s\" %s >> \"%s\" 2>&1\r\n",
		data.GCPath, strings.Join(gcArgs, " "), data.LogPath)
	return b.String()
}

// supervisorTaskWrapperBinary extracts the gc binary path a wrapper was
// installed from. It is the Task Scheduler analog of
// supervisorSystemdExecStartBinary, and exists for the same reason: refusing to
// overwrite an install made from a different binary without --force.
//
// It reads the marker comment rather than the invocation line so the guard does
// not depend on batch quoting rules. An empty return means "unknown", which
// callers treat as "no drift detected" -- the guard must never block an install
// because it could not parse a file it wrote itself.
func supervisorTaskWrapperBinary(wrapper string) string {
	return supervisorTaskWrapperMarkerValue(wrapper, supervisorTaskWrapperBinaryMarker)
}

// supervisorTaskWrapperGCHome extracts the GC_HOME a wrapper was installed for,
// so the stale sweep can tell whether that home still exists.
func supervisorTaskWrapperGCHome(wrapper string) string {
	return supervisorTaskWrapperMarkerValue(wrapper, supervisorTaskWrapperHomeMarker)
}

func supervisorTaskWrapperMarkerValue(wrapper, marker string) string {
	for _, line := range strings.Split(wrapper, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, marker) {
			return strings.TrimSpace(strings.TrimPrefix(line, marker))
		}
	}
	return ""
}

// parseSchtasksTaskNames extracts task names from `schtasks /Query /FO CSV /NH`
// output.
//
// The CSV carries "TaskName","Next Run Time","Status" per row, with the task
// name leading. Rows are filtered to gc's own prefix by the caller; this only
// handles the quoting, including the repeated header schtasks emits when
// folders are present.
func parseSchtasksTaskNames(csv string) []string {
	var names []string
	for _, line := range strings.Split(csv, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			continue
		}
		field := line
		if strings.HasPrefix(field, `"`) {
			// Take the first quoted field.
			if end := strings.Index(field[1:], `"`); end >= 0 {
				field = field[1 : end+1]
			} else {
				field = strings.Trim(field, `"`)
			}
		} else if idx := strings.Index(field, ","); idx >= 0 {
			field = field[:idx]
		}
		field = strings.TrimSpace(field)
		// schtasks repeats the header row per folder; it is not a task.
		if field == "" || field == "TaskName" {
			continue
		}
		names = append(names, field)
	}
	return names
}

// supervisorTaskIsGCOwned reports whether a task name belongs to gc, so the
// sweep can never touch an unrelated task that merely sorts nearby.
func supervisorTaskIsGCOwned(name string) bool {
	base := strings.TrimPrefix(name, `\`)
	return base == supervisorTaskPrefix ||
		strings.HasPrefix(base, supervisorTaskPrefix+" (") ||
		strings.HasPrefix(base, supervisorTaskPrefix+" "+supervisorKeepaliveTaskSuffix)
}

// supervisorTaskIsIsolated reports whether a gc-owned task name carries a
// GC_HOME suffix.
//
// The unsuffixed "GasCity Supervisor" is the user's real supervisor and must
// never be swept, mirroring the systemd sweep's rule that the bare
// gascity-supervisor.service is off limits.
func supervisorTaskIsIsolated(name string) bool {
	base := strings.TrimPrefix(name, `\`)
	return supervisorTaskIsGCOwned(base) && strings.HasSuffix(base, ")")
}

// schtasksRun invokes schtasks.exe and returns its combined output.
var schtasksRun = func(args ...string) (string, error) {
	cmd := exec.Command("schtasks", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// installSupervisorSchtasks registers the logon-triggered supervisor task and
// its keepalive companion.
func installSupervisorSchtasks(data *supervisorServiceData, stdout, stderr io.Writer) int {
	sweepStaleIsolatedSupervisorServices(stderr)

	wrapperPath := supervisorTaskWrapperPath()
	existing, err := os.ReadFile(wrapperPath)
	hadCurrent := err == nil
	if err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(stderr, "gc supervisor install: reading existing task wrapper: %v\n", err) //nolint:errcheck
		return 1
	}
	if hadCurrent && !supervisorInstallForce {
		if existingBinary := supervisorTaskWrapperBinary(string(existing)); existingBinary != "" &&
			!supervisorSameBinary(existingBinary, data.GCPath) {
			fmt.Fprintf(stderr, //nolint:errcheck
				"gc supervisor install: existing task %q runs binary %q but the current gc binary resolves to %q; "+
					"refusing to overwrite a task installed from a different binary. "+
					"Install gc to a stable location first, then rerun 'gc supervisor install'. "+
					"To override, pass --force.\n",
				supervisorTaskName(), existingBinary, data.GCPath)
			return 1
		}
	}

	if err := os.MkdirAll(supervisor.RuntimeDir(), 0o700); err != nil {
		fmt.Fprintf(stderr, "gc supervisor install: %v\n", err) //nolint:errcheck
		return 1
	}
	if err := ensureSupervisorServiceLogDir(data.LogPath); err != nil {
		fmt.Fprintf(stderr, "gc supervisor install: %v\n", err) //nolint:errcheck
		return 1
	}

	if err := writeSupervisorServiceFile(wrapperPath,
		[]byte(renderSupervisorTaskWrapper(data, "supervisor", "run"))); err != nil {
		fmt.Fprintf(stderr, "gc supervisor install: writing task wrapper: %v\n", err) //nolint:errcheck
		return 1
	}
	if err := writeSupervisorServiceFile(supervisorKeepaliveWrapperPath(),
		[]byte(renderSupervisorTaskWrapper(data, "supervisor", "ensure"))); err != nil {
		fmt.Fprintf(stderr, "gc supervisor install: writing keepalive wrapper: %v\n", err) //nolint:errcheck
		return 1
	}

	// The supervisor task carries NO scheduler restart. Task Scheduler cannot
	// exempt an exit code, so its restart would crash-loop a port collision;
	// the keepalive task below owns restart instead.
	if out, err := schtasksRun("/Create", "/F",
		"/TN", supervisorTaskName(),
		"/TR", `"`+supervisorTaskWrapperPath()+`"`,
		"/SC", "ONLOGON"); err != nil {
		fmt.Fprintf(stderr, //nolint:errcheck
			"gc supervisor install: registering task %q: %v\n%s\n",
			supervisorTaskName(), err, strings.TrimSpace(out))
		return 1
	}
	if out, err := schtasksRun("/Create", "/F",
		"/TN", supervisorKeepaliveTaskName(),
		"/TR", `"`+supervisorKeepaliveWrapperPath()+`"`,
		"/SC", "MINUTE",
		"/MO", fmt.Sprint(supervisorKeepaliveMinutes)); err != nil {
		fmt.Fprintf(stderr, //nolint:errcheck
			"gc supervisor install: registering keepalive task %q: %v\n%s\n",
			supervisorKeepaliveTaskName(), err, strings.TrimSpace(out))
		return 1
	}

	fmt.Fprintf(stdout, "Installed scheduled task %q (runs at logon)\n", supervisorTaskName()) //nolint:errcheck
	fmt.Fprintf(stdout, "Installed keepalive task %q (every %d minutes)\n",                    //nolint:errcheck
		supervisorKeepaliveTaskName(), supervisorKeepaliveMinutes)
	fmt.Fprintf(stdout, "Start it now with: gc supervisor start\n") //nolint:errcheck
	return 0
}

// uninstallSupervisorSchtasks removes both tasks and their wrappers.
func uninstallSupervisorSchtasks(data *supervisorServiceData, stdout, stderr io.Writer) int {
	_ = data
	failed := false
	for _, name := range []string{supervisorKeepaliveTaskName(), supervisorTaskName()} {
		if out, err := schtasksRun("/Delete", "/F", "/TN", name); err != nil {
			// A task that was never installed is not a failure: uninstall is
			// expected to be runnable on a machine that never ran install.
			if !supervisorTaskMissing(out) {
				fmt.Fprintf(stderr, //nolint:errcheck
					"gc supervisor uninstall: removing task %q: %v\n%s\n",
					name, err, strings.TrimSpace(out))
				failed = true
				continue
			}
		}
		fmt.Fprintf(stdout, "Removed scheduled task %q\n", name) //nolint:errcheck
	}
	for _, path := range []string{supervisorTaskWrapperPath(), supervisorKeepaliveWrapperPath()} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(stderr, "gc supervisor uninstall: removing %s: %v\n", path, err) //nolint:errcheck
			failed = true
		}
	}
	if failed {
		return 1
	}
	return 0
}

// supervisorTaskMissing reports whether schtasks failed only because the task
// does not exist.
func supervisorTaskMissing(out string) bool {
	lowered := strings.ToLower(out)
	return strings.Contains(lowered, "cannot find the file specified") ||
		strings.Contains(lowered, "does not exist") ||
		strings.Contains(lowered, "the system cannot find")
}

// sweepStaleIsolatedSupervisorSchtasks removes tasks belonging to isolated
// GC_HOMEs that no longer exist. It fills what was a default: no-op in
// sweepStaleIsolatedSupervisorServices.
func sweepStaleIsolatedSupervisorSchtasks(stderr io.Writer) {
	out, err := schtasksRun("/Query", "/FO", "CSV", "/NH")
	if err != nil {
		// No Task Scheduler access, or none registered: nothing to sweep.
		return
	}
	ownTask := supervisorTaskName()
	ownKeepalive := supervisorKeepaliveTaskName()
	for _, name := range parseSchtasksTaskNames(out) {
		if !supervisorTaskIsIsolated(name) {
			continue
		}
		base := strings.TrimPrefix(name, `\`)
		if base == ownTask || base == ownKeepalive {
			continue
		}
		gcHome, ok := supervisorTaskGCHome(base)
		if !ok || !supervisorServiceGCHomeMissing(gcHome) {
			continue
		}
		if _, err := schtasksRun("/Delete", "/F", "/TN", name); err != nil {
			fmt.Fprintf(stderr, "gc supervisor: removing stale isolated supervisor task %s: %v\n", name, err) //nolint:errcheck
			continue
		}
		fmt.Fprintf(stderr, //nolint:errcheck
			"gc supervisor: removed stale isolated supervisor task %s (GC_HOME %s no longer exists)\n",
			name, gcHome)
	}
}

// supervisorTaskGCHome recovers the GC_HOME a registered task was installed
// for, by reading the wrapper its action points at.
//
// Returning false means "could not determine", which the sweep treats as "leave
// it alone". Deleting a task whose home cannot be read would risk removing a
// live supervisor's task on the strength of a parse failure.
func supervisorTaskGCHome(taskName string) (string, bool) {
	out, err := schtasksRun("/Query", "/TN", taskName, "/FO", "LIST", "/V")
	if err != nil {
		return "", false
	}
	wrapperPath := supervisorTaskActionPath(out)
	if wrapperPath == "" {
		return "", false
	}
	content, err := os.ReadFile(wrapperPath)
	if err != nil {
		return "", false
	}
	home := supervisorTaskWrapperGCHome(string(content))
	if home == "" {
		return "", false
	}
	return home, true
}

// supervisorTaskActionPath pulls the wrapper path out of `schtasks /Query /V
// /FO LIST` output, whose "Task To Run:" line carries the action.
func supervisorTaskActionPath(query string) string {
	const key = "task to run:"
	for _, line := range strings.Split(query, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < len(key) || !strings.EqualFold(trimmed[:len(key)], key) {
			continue
		}
		value := strings.TrimSpace(trimmed[len(key):])
		value = strings.Trim(value, `"`)
		return value
	}
	return ""
}
