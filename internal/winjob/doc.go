// Package winjob wraps Windows Job Objects — the platform's
// process-tree containment primitive and the closest analogue of a
// systemd slice/cgroup (see engdocs/design/windows-systemd-parity.md,
// D1). A job with KillOnClose guarantees no member process outlives the
// last handle to the job, which is the structural fix for the orphaned
// test-process incidents (gw-qhs, gw-8g5): Windows never tears down
// process trees on its own.
//
// All functionality is Windows-only and lives in windows build-tagged
// files; this file exists so the package compiles (empty) everywhere.
package winjob
