package k8s

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

// ListSessionsForAdmission includes nonterminal provisioned pods, not only
// Running pods: pending pods may launch a model after their caller exits.
func (p *Provider) ListSessionsForAdmission(ctx context.Context) ([]string, error) {
	pods, err := p.ops.listPods(ctx, "app=gc-agent", "")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			continue
		}
		name := pod.Annotations["gc-session-name"]
		if name == "" {
			name = pod.Labels["gc-session"]
		}
		if name == "" {
			name = pod.Name
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

// ListSessionsForAdmission preserves the raw provider's pending-pod census.
func (s *seamBackedProvider) ListSessionsForAdmission(ctx context.Context) ([]string, error) {
	return s.raw.ListSessionsForAdmission(ctx)
}
