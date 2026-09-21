package k8s

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdmissionCensusCountsPendingPods(t *testing.T) {
	ops := newFakeK8sOps()
	for _, phase := range []corev1.PodPhase{corev1.PodPending, corev1.PodRunning, corev1.PodUnknown, corev1.PodFailed, corev1.PodSucceeded} {
		name := string(phase)
		ops.pods[name] = &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"app": "gc-agent", "gc-session": name}}, Status: corev1.PodStatus{Phase: phase}}
	}
	p := newProviderWithOps(ops)
	names, err := p.ListSessionsForAdmission(context.Background())
	if err != nil || len(names) != 3 {
		t.Fatalf("names=%v err=%v, want all three nonterminal pods", names, err)
	}
	wrapped := &seamBackedProvider{raw: p}
	names, err = wrapped.ListSessionsForAdmission(context.Background())
	if err != nil || len(names) != 3 {
		t.Fatalf("seam wrapper lost pending census: names=%v err=%v", names, err)
	}
}
