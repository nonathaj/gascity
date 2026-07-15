package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
	"github.com/gastownhall/gascity/internal/fsys"
	sessionexec "github.com/gastownhall/gascity/internal/runtime/exec"
)

// packRuntimesDoctorCheck verifies every pack-declared runtime
// ([runtimes.<name>] in pack.toml) is installed and answers the RPP
// protocol handshake. A missing protocol op is the documented version-0
// floor, not a failure (RUNTIME-RPP-008); a present-but-broken handshake
// is. Deeper conformance is `gc runtime check <name>`.
type packRuntimesDoctorCheck struct {
	cfg *config.City
}

func newPackRuntimesDoctorCheck(cfg *config.City) *packRuntimesDoctorCheck {
	return &packRuntimesDoctorCheck{cfg: cfg}
}

// Name implements doctor.Check.
func (*packRuntimesDoctorCheck) Name() string { return "pack-runtimes" }

// CanFix implements doctor.Check. Installation belongs to the pack's own
// install/doctor flow, so this check is diagnostic-only.
func (*packRuntimesDoctorCheck) CanFix() bool { return false }

// WarmupEligible implements doctor.Check. Handshakes fork the runtime
// executable, so the check stays out of `gc start` warm-up.
func (*packRuntimesDoctorCheck) WarmupEligible() bool { return false }

// Fix implements doctor.Check.
func (*packRuntimesDoctorCheck) Fix(_ *doctor.CheckContext) error { return nil }

// Run implements doctor.Check.
func (c *packRuntimesDoctorCheck) Run(_ *doctor.CheckContext) *doctor.CheckResult {
	if c.cfg == nil || len(c.cfg.Runtimes) == 0 {
		return okCheck("pack-runtimes", "no pack-declared runtimes")
	}
	names := make([]string, 0, len(c.cfg.Runtimes))
	for name := range c.cfg.Runtimes {
		names = append(names, name)
	}
	sort.Strings(names)

	var failures []string
	for _, name := range names {
		rt := c.cfg.Runtimes[name]
		if detail := packRuntimeInstallFailure(rt); detail != "" {
			failures = append(failures, detail)
			continue
		}
		if _, err := sessionexec.NewProvider(rt.Command).Protocol(); err != nil {
			failures = append(failures, fmt.Sprintf("runtime %q (pack %q): protocol handshake failed: %v", rt.Name, rt.PackName, err))
		}
	}
	if len(failures) > 0 {
		return errorCheck("pack-runtimes",
			fmt.Sprintf("%d of %d pack runtime(s) unusable", len(failures), len(names)),
			"install the runtime executable (the pack's install step) or fix the [runtimes.<name>] command in pack.toml",
			failures)
	}
	return okCheck("pack-runtimes", fmt.Sprintf("%d pack runtime(s) installed and handshaking", len(names)))
}

// packRuntimeInstallFailure reports why a declared runtime executable is
// not invocable, or "" when it is. Commands containing a path separator
// were resolved at composition; bare names resolve on PATH like the exec
// provider does at session start.
func packRuntimeInstallFailure(rt config.DiscoveredRuntime) string {
	if !runtimeCommandIsPath(runtime.GOOS, rt.Command) {
		if _, err := exec.LookPath(rt.Command); err != nil {
			return fmt.Sprintf("runtime %q (pack %q): %q not found on PATH", rt.Name, rt.PackName, rt.Command)
		}
		return ""
	}
	info, err := os.Stat(rt.Command)
	if err != nil {
		return fmt.Sprintf("runtime %q (pack %q): executable not found at %s", rt.Name, rt.PackName, rt.Command)
	}
	if !fsys.IsExecutableMode(info.Mode()) {
		return fmt.Sprintf("runtime %q (pack %q): %s is not an executable file", rt.Name, rt.PackName, rt.Command)
	}
	return ""
}

// runtimeCommandIsPath reports whether command names an on-disk location
// rather than a bare name to resolve on PATH. On Windows a path can be
// spelled with backslashes or a drive prefix ("C:\tools\dolt.exe") and
// contain no forward slash at all, so checking "/" alone misclassifies it
// as a PATH lookup.
func runtimeCommandIsPath(goos, command string) bool {
	if strings.ContainsRune(command, '/') {
		return true
	}
	if goos == "windows" {
		if strings.ContainsRune(command, '\\') {
			return true
		}
		// Drive-relative or drive-absolute spelling ("C:...").
		if len(command) >= 2 && command[1] == ':' {
			return true
		}
	}
	return false
}
