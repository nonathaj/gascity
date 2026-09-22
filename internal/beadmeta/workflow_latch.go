package beadmeta

import (
	"slices"
	"strings"
)

// IsWorkflowLatch reports whether metadata describes topology rather than an
// executable work unit. Legacy workflow roots remain executable; only graph.v2
// roots are latches. Scope and spec nodes are always structural.
func IsWorkflowLatch(metadata map[string]string) bool {
	kind := strings.TrimSpace(metadata[KindMetadataKey])
	if !slices.Contains(WorkflowTopologyKinds, kind) {
		return false
	}
	return kind != KindWorkflow || strings.EqualFold(strings.TrimSpace(metadata[FormulaContractMetadataKey]), FormulaContractGraphV2)
}
