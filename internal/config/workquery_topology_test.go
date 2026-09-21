package config

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/gastownhall/gascity/internal/beadmeta"
)

// A backlog of workflow containers must neither hide executable work beyond
// the candidate limit nor count as capacity demand of its own.
func TestPoolQuerySkipsTopologyBeforeLimit(t *testing.T) {
	var rows []string
	for i := 0; i < 25; i++ {
		rows = append(rows, fmt.Sprintf(`{"id":"root-%d","metadata":{"gc.kind":"workflow","gc.formula_contract":"graph.v2","gc.routed_to":"worker"}}`, i))
	}
	for _, topo := range []QueryTopology{singleStoreTopology(), federatedTopology()} {
		for _, withTask := range []bool{false, true} {
			t.Run(fmt.Sprintf("federated=%v/task=%v", topo.FederatedReady, withTask), func(t *testing.T) {
				payload := append([]string(nil), rows...)
				if withTask {
					payload = append(payload, `{"id":"real-task","metadata":{"gc.routed_to":"worker"}}`)
				}
				fake := `#!/bin/sh
case "$*" in
  *gc.run_target*) printf '[]'; exit 0 ;;
esac
limit=0
for arg in "$@"; do
  case "$arg" in --limit=*) limit=${arg#--limit=} ;; esac
done
printf '%s' '` + "[" + strings.Join(payload, ",") + "]" + `' | jq --argjson n "$limit" 'if $n > 0 then .[:$n] else . end'
`
				result := runGeneratedQueryWithBD(t, poolDemandFirstRowFunctionScript(topo)+`probe_pool_demand worker; printf '[]'`, nil, fake, fake)
				if result.exit != 0 {
					t.Fatalf("query failed: %s", result.stderr)
				}
				out := result.stdout
				var got []struct{ ID string }
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatal(err)
				}
				want := 0
				if withTask {
					want = 1
				}
				if len(got) != want || (want == 1 && got[0].ID != "real-task") {
					t.Errorf("claim window = %s; want %d executable tasks", out, want)
				}
				result = runGeneratedQueryWithBD(t, poolDemandCountShell("worker", topo), nil, fake, fake)
				if result.exit != 0 {
					t.Fatalf("count failed: %s", result.stderr)
				}
				count := result.stdout
				if strings.TrimSpace(count) != fmt.Sprint(want) {
					t.Errorf("demand = %s; want %d", count, want)
				}
			})
		}
	}
}

func TestWorkflowLatchShellMatchesDomainPredicate(t *testing.T) {
	kinds := append([]string{"", "unknown", beadmeta.KindTask}, beadmeta.WorkflowTopologyKinds...)
	kinds = append(kinds, beadmeta.ControlKinds...)
	for _, kind := range kinds {
		for _, contract := range []string{"", "graph.v1", "graph.v2", " GRAPH.V2 "} {
			metadata := map[string]string{beadmeta.KindMetadataKey: " " + kind + " ", beadmeta.FormulaContractMetadataKey: contract}
			payload, err := json.Marshal([]map[string]any{{"metadata": metadata}})
			if err != nil {
				t.Fatal(err)
			}
			got := runJQFilter(t, `[.[]`+excludeWorkflowLatchesJQClause()+`] | length`, string(payload))
			want := "1"
			if beadmeta.IsWorkflowLatch(metadata) {
				want = "0"
			}
			if got != want {
				t.Errorf("kind=%q contract=%q: shell=%s, want %s", kind, contract, got, want)
			}
		}
	}
}
