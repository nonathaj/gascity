package auto

import (
	"context"

	"github.com/gastownhall/gascity/internal/runtime"
)

// ListSessionsForAdmission preserves admission-specific census semantics on
// both sides of the transport router, including partial-observation errors.
func (p *Provider) ListSessionsForAdmission(ctx context.Context) ([]string, error) {
	defaults, dErr := runtime.ListSessionsForAdmission(ctx, p.defaultSP)
	acp, aErr := runtime.ListSessionsForAdmission(ctx, p.acpSP)
	return runtime.MergeBackendListResults(
		runtime.BackendListResult{Label: "default", Names: defaults, Err: dErr},
		runtime.BackendListResult{Label: "acp", Names: acp, Err: aErr},
	)
}
