package runtime

import "context"

// AdmissionSessionLister supplies a census suitable for launch admission when
// ListRunning alone is insufficient (for example, provisioned pending pods or
// a confirmed-absent tmux server). Partial observations must return an error.
type AdmissionSessionLister interface {
	ListSessionsForAdmission(context.Context) ([]string, error)
}

// ListSessionsForAdmission returns every runtime identity consuming a launch
// slot. Providers without a specialized census use their complete live list.
func ListSessionsForAdmission(ctx context.Context, sp Provider) ([]string, error) {
	if census, ok := sp.(AdmissionSessionLister); ok {
		return census.ListSessionsForAdmission(ctx)
	}
	return sp.ListRunning("")
}
