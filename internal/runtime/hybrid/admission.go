package hybrid

import (
	"context"

	"github.com/gastownhall/gascity/internal/runtime"
)

// ListSessionsForAdmission preserves complete launch-census semantics across
// local and remote backends, rather than discarding pending runtime identities.
func (p *Provider) ListSessionsForAdmission(ctx context.Context) ([]string, error) {
	local, lErr := runtime.ListSessionsForAdmission(ctx, p.local)
	remote, rErr := runtime.ListSessionsForAdmission(ctx, p.remote)
	return runtime.MergeBackendListResults(
		runtime.BackendListResult{Label: "local", Names: local, Err: lErr},
		runtime.BackendListResult{Label: "remote", Names: remote, Err: rErr},
	)
}
