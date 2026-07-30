// Package herdrversion centralizes herdr version detection and the
// agent-start interface era, mirroring internal/doltversion.
//
// Why it exists: gascity's herdr provider shells out to the `herdr` CLI, whose
// `agent start` subcommand changed shape in 0.7.5. Through 0.7.4 it accepted a
// headless launch form — `agent start <name> --no-focus [--tab <id>] [--cwd
// <dir>] [--env K=V…] -- <argv…>`. From 0.7.5 it instead takes `agent start
// <name> --kind <KIND> --pane <ID>` and launches a *detected* agent into an
// existing pane, with no way to carry cwd/env/argv. gascity's runtime needs to
// launch an arbitrary command with env and a working dir, so the 0.7.5+ CLI
// cannot be used as a drop-in; the provider launches via pane verbs instead.
//
// This package lets the provider (and gc doctor) detect which interface the
// installed herdr exposes and either adapt or fail clearly — instead of the
// silent start→quarantine loop an unrecognized CLI produces.
package herdrversion

import (
	"fmt"
	"strconv"
	"strings"
)

// LegacyStartMax is the highest herdr version whose `agent start` accepts the
// legacy headless form (--no-focus/--cwd/--env/-- <argv>). 0.7.5 removed it.
const LegacyStartMax = "0.7.4"

var (
	// ErrUnrecognized reports a version string that could not be parsed.
	ErrUnrecognized = fmt.Errorf("unrecognized herdr version")
)

// Info is the parsed semantic version of the installed `herdr` binary.
type Info struct {
	Major, Minor, Patch int
	Raw                 string
	PreRelease          bool
}

// Parse parses the version token from `herdr --version` output ("herdr 0.7.5"
// or a bare "0.7.5"). Build metadata after patch ("+…") is ignored; a
// pre-release suffix ("-rc1") is preserved on Raw and flagged.
func Parse(out string) (Info, error) {
	out = strings.TrimSpace(out)
	if out == "" {
		return Info{}, fmt.Errorf("%w: empty version output", ErrUnrecognized)
	}
	if i := strings.IndexByte(out, '\n'); i >= 0 {
		out = out[:i]
	}
	out = strings.TrimSpace(out)
	if prefix := "herdr "; strings.HasPrefix(strings.ToLower(out), prefix) {
		out = out[len(prefix):]
	}
	if i := strings.IndexAny(out, " \t"); i >= 0 {
		out = out[:i]
	}
	out = strings.TrimPrefix(out, "v")

	core := out
	if i := strings.IndexByte(core, '+'); i >= 0 {
		core = core[:i]
	}
	preRelease := false
	if i := strings.IndexByte(core, '-'); i >= 0 {
		preRelease = true
		core = core[:i]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 {
		return Info{}, fmt.Errorf("%w: %q", ErrUnrecognized, out)
	}
	num := func(s string) (int, error) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, fmt.Errorf("%w: bad component %q in %q", ErrUnrecognized, s, out)
		}
		return n, nil
	}
	major, err := num(parts[0])
	if err != nil {
		return Info{}, err
	}
	minor, err := num(parts[1])
	if err != nil {
		return Info{}, err
	}
	patch := 0
	if len(parts) > 2 {
		if patch, err = num(parts[2]); err != nil {
			return Info{}, err
		}
	}
	raw := fmt.Sprintf("%d.%d.%d", major, minor, patch)
	if preRelease {
		raw = out
	}
	return Info{Major: major, Minor: minor, Patch: patch, Raw: raw, PreRelease: preRelease}, nil
}

// Compare returns -1, 0, or 1 as a is less than, equal to, or greater than b.
func Compare(a, b Info) int {
	switch {
	case a.Major != b.Major:
		if a.Major < b.Major {
			return -1
		}
		return 1
	case a.Minor != b.Minor:
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	case a.Patch != b.Patch:
		if a.Patch < b.Patch {
			return -1
		}
		return 1
	}
	return 0
}

// SupportsLegacyStart reports whether this herdr's `agent start` accepts the
// legacy headless form gascity's original provider targeted (≤ 0.7.4).
func (i Info) SupportsLegacyStart() bool {
	max, err := Parse(LegacyStartMax)
	if err != nil {
		return false
	}
	return Compare(i, max) <= 0
}
