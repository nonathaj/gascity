package main

import (
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
)

func TestDefaultDemandExcludesWorkflowLatches(t *testing.T) {
	for _, tc := range []struct {
		kind, contract string
		want           int
	}{
		{beadmeta.KindWorkflow, beadmeta.FormulaContractGraphV2, 0},
		{beadmeta.KindScope, "", 0},
		{beadmeta.KindSpec, "", 0},
		{beadmeta.KindWorkflow, "", 1},
		{beadmeta.KindTask, beadmeta.FormulaContractGraphV2, 1},
		{beadmeta.KindCheck, beadmeta.FormulaContractGraphV2, 1},
	} {
		t.Run(tc.kind+"/"+tc.contract, func(t *testing.T) {
			store := beads.NewMemStore()
			_, err := store.Create(beads.Bead{ID: "work", Type: "task", Status: "open", Metadata: map[string]string{
				beadmeta.KindMetadataKey:            tc.kind,
				beadmeta.FormulaContractMetadataKey: tc.contract,
				beadmeta.RoutedToMetadataKey:        "worker",
			}})
			if err != nil {
				t.Fatal(err)
			}
			counts, _, errs := defaultScaleCheckCounts([]defaultScaleCheckTarget{{template: "worker", store: store, storeKey: "city"}})
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			if counts["worker"] != tc.want {
				t.Fatalf("native demand = %d; want %d", counts["worker"], tc.want)
			}
		})
	}
}
