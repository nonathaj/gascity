package api

import (
	"log"
	"os"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/execshim"
)

// componentVersions holds the versions of the external binaries the
// supervisor drives. A field is empty when the corresponding binary is
// unavailable or its probe failed; callers surface empty as an absent,
// omitempty wire field rather than a guessed value.
type componentVersions struct {
	Dolt  string
	Beads string
}

// resolveComponentVersions returns the dolt engine and bd CLI versions the
// supervisor drives, probing each binary at most once per process. Binary
// versions are immutable for the life of the process that launched them, so a
// single probe is both cheaper than re-probing on the hot status path and
// semantically correct: the running supervisor keeps driving the binaries it
// resolved until it restarts. The actual subprocess execution lives in
// internal/beads (bd/dolt are confined there by architectural rule); this
// layer only caches the result and logs probe failures server-side.
func (s *Server) resolveComponentVersions() componentVersions {
	s.componentVersionsOnce.Do(func() {
		if s.componentVersionsProbe != nil {
			s.componentVersionsValue = s.componentVersionsProbe()
			return
		}
		// A test binary that reaches the status/config path without injecting a
		// probe must NOT spawn the real `dolt version` / `bd version`
		// subprocesses: unit tests spawning external binaries is a hygiene
		// violation, and doing so concurrently under the api suite's
		// parallelism has tripped a Go runtime crash on Windows ("found pointer
		// to free object" inside os/exec). Leave versions empty — the same
		// result a host without dolt/bd would produce. Production is unaffected.
		if len(os.Args) > 0 && execshim.IsGoTestExecutable(os.Args[0]) {
			s.componentVersionsValue = componentVersions{}
			return
		}
		s.componentVersionsValue = probeComponentVersions(beads.ProbeDoltVersion, beads.ProbeBDVersion)
	})
	return s.componentVersionsValue
}

// probeComponentVersions resolves both binary versions. A failed probe leaves
// that field empty and logs the cause server-side so a missing version is
// diagnosable rather than mystifying (consistent with the "don't swallow
// errors" rule). probeDolt/probeBD are injectable for tests.
func probeComponentVersions(probeDolt, probeBD func() (string, error)) componentVersions {
	var cv componentVersions
	if v, err := probeDolt(); err != nil {
		log.Printf("status: dolt version probe failed: %v", err)
	} else {
		cv.Dolt = v
	}
	if v, err := probeBD(); err != nil {
		log.Printf("status: bd version probe failed: %v", err)
	} else {
		cv.Beads = v
	}
	return cv
}
