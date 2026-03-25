package formula

import (
	"encoding/json"
	"testing"
)

func TestApplyRalph_Basic(t *testing.T) {
	steps := []*Step{
		{
			ID:          "implement",
			Title:       "Implement widget",
			Description: "Make the code changes.",
			Type:        "task",
			DependsOn:   []string{"design"},
			Needs:       []string{"setup"},
			Labels:      []string{"frontend"},
			Metadata: map[string]string{
				"custom": "value",
			},
			Ralph: &RalphSpec{
				MaxAttempts: 3,
				Check: &RalphCheckSpec{
					Mode:    "exec",
					Path:    ".gascity/checks/widget.sh",
					Timeout: "2m",
				},
			},
		},
	}

	got, err := ApplyRalph(steps)
	if err != nil {
		t.Fatalf("ApplyRalph failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (control + iteration)", len(got))
	}

	control := got[0]
	iteration := got[1]

	// Control bead.
	if control.ID != "implement" {
		t.Fatalf("control.ID = %q, want implement", control.ID)
	}
	if control.Metadata["gc.kind"] != "ralph" {
		t.Errorf("control gc.kind = %q, want ralph", control.Metadata["gc.kind"])
	}
	if control.Metadata["gc.max_attempts"] != "3" {
		t.Errorf("control gc.max_attempts = %q, want 3", control.Metadata["gc.max_attempts"])
	}
	if control.Metadata["gc.check_mode"] != "exec" {
		t.Errorf("control gc.check_mode = %q, want exec", control.Metadata["gc.check_mode"])
	}
	if control.Metadata["gc.check_path"] != ".gascity/checks/widget.sh" {
		t.Errorf("control gc.check_path = %q, want .gascity/checks/widget.sh", control.Metadata["gc.check_path"])
	}
	if control.Metadata["gc.check_timeout"] != "2m" {
		t.Errorf("control gc.check_timeout = %q, want 2m", control.Metadata["gc.check_timeout"])
	}
	if control.Metadata["gc.control_epoch"] != "1" {
		t.Errorf("control gc.control_epoch = %q, want 1", control.Metadata["gc.control_epoch"])
	}
	if control.Metadata["gc.source_step_spec"] == "" {
		t.Fatal("control missing gc.source_step_spec")
	}

	// Control blocks on the iteration (not a check bead).
	wantControlNeeds := map[string]bool{"setup": true, "implement.iteration.1": true}
	if len(control.Needs) != 2 {
		t.Fatalf("control.Needs = %v, want two entries", control.Needs)
	}
	for _, need := range control.Needs {
		if !wantControlNeeds[need] {
			t.Errorf("control.Needs contains unexpected %q", need)
		}
	}

	// Iteration bead.
	if iteration.ID != "implement.iteration.1" {
		t.Fatalf("iteration.ID = %q, want implement.iteration.1", iteration.ID)
	}
	if iteration.Metadata["gc.attempt"] != "1" {
		t.Errorf("iteration gc.attempt = %q, want 1", iteration.Metadata["gc.attempt"])
	}
	if iteration.Metadata["gc.ralph_step_id"] != "implement" {
		t.Errorf("iteration gc.ralph_step_id = %q, want implement", iteration.Metadata["gc.ralph_step_id"])
	}
	if iteration.Metadata["custom"] != "value" {
		t.Errorf("iteration custom metadata = %q, want value", iteration.Metadata["custom"])
	}

	// Iteration inherits external deps.
	if len(iteration.DependsOn) != 1 || iteration.DependsOn[0] != "design" {
		t.Errorf("iteration.DependsOn = %v, want [design]", iteration.DependsOn)
	}
	if len(iteration.Needs) != 1 || iteration.Needs[0] != "setup" {
		t.Errorf("iteration.Needs = %v, want [setup]", iteration.Needs)
	}
}

func TestApplyRalph_FrozenSpecRoundTrips(t *testing.T) {
	original := &Step{
		ID:    "converge",
		Title: "Converge",
		Type:  "task",
		Ralph: &RalphSpec{
			MaxAttempts: 5,
			Check: &RalphCheckSpec{
				Mode: "exec",
				Path: "check.sh",
			},
		},
		Children: []*Step{
			{ID: "apply", Title: "Apply changes"},
		},
	}

	got, err := ApplyRalph([]*Step{original})
	if err != nil {
		t.Fatalf("ApplyRalph failed: %v", err)
	}

	control := got[0]
	var frozen Step
	if err := json.Unmarshal([]byte(control.Metadata["gc.source_step_spec"]), &frozen); err != nil {
		t.Fatalf("unmarshal frozen spec: %v", err)
	}
	if frozen.ID != "converge" {
		t.Errorf("frozen ID = %q, want converge", frozen.ID)
	}
	if frozen.Ralph == nil || frozen.Ralph.MaxAttempts != 5 {
		t.Errorf("frozen ralph = %+v, want max_attempts=5", frozen.Ralph)
	}
	if len(frozen.Children) != 1 || frozen.Children[0].ID != "apply" {
		t.Errorf("frozen children = %v, want [apply]", frozen.Children)
	}
}

func TestApplyRalph_NestedWithChildren(t *testing.T) {
	steps := []*Step{
		{
			ID:    "review-loop",
			Title: "Review loop",
			Type:  "task",
			Ralph: &RalphSpec{
				MaxAttempts: 3,
				Check:       &RalphCheckSpec{Mode: "exec", Path: "check.sh"},
			},
			Children: []*Step{
				{ID: "review", Title: "Review"},
				{ID: "apply", Title: "Apply", Needs: []string{"review"}},
			},
		},
	}

	got, err := ApplyRalph(steps)
	if err != nil {
		t.Fatalf("ApplyRalph failed: %v", err)
	}

	// Expect: control + iteration scope + 2 body children = 4
	if len(got) != 4 {
		names := make([]string, len(got))
		for i, s := range got {
			names[i] = s.ID
		}
		t.Fatalf("len(got) = %d, want 4; steps: %v", len(got), names)
	}

	control := got[0]
	iteration := got[1]

	if control.Metadata["gc.kind"] != "ralph" {
		t.Errorf("control gc.kind = %q, want ralph", control.Metadata["gc.kind"])
	}
	if iteration.Metadata["gc.kind"] != "scope" {
		t.Errorf("iteration gc.kind = %q, want scope", iteration.Metadata["gc.kind"])
	}
	if iteration.ID != "review-loop.iteration.1" {
		t.Errorf("iteration.ID = %q, want review-loop.iteration.1", iteration.ID)
	}

	// Body children should be namespaced under the iteration.
	review := got[2]
	apply := got[3]
	if review.ID != "review-loop.iteration.1.review" {
		t.Errorf("review.ID = %q, want review-loop.iteration.1.review", review.ID)
	}
	if apply.ID != "review-loop.iteration.1.apply" {
		t.Errorf("apply.ID = %q, want review-loop.iteration.1.apply", apply.ID)
	}

	// apply should depend on review (namespaced).
	if len(apply.Needs) < 1 {
		t.Fatalf("apply.Needs = %v, want at least review-loop.iteration.1.review", apply.Needs)
	}
	foundReviewDep := false
	for _, n := range apply.Needs {
		if n == "review-loop.iteration.1.review" {
			foundReviewDep = true
		}
	}
	if !foundReviewDep {
		t.Errorf("apply.Needs = %v, missing review-loop.iteration.1.review", apply.Needs)
	}
}

func TestApplyRalph_PreservesNonRalphSteps(t *testing.T) {
	steps := []*Step{
		{ID: "setup", Title: "Setup"},
		{ID: "work", Title: "Work", Ralph: &RalphSpec{
			MaxAttempts: 2,
			Check:       &RalphCheckSpec{Mode: "exec", Path: "check.sh"},
		}},
		{ID: "cleanup", Title: "Cleanup"},
	}

	got, err := ApplyRalph(steps)
	if err != nil {
		t.Fatalf("ApplyRalph failed: %v", err)
	}
	// setup + (control + iteration) + cleanup = 4
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4", len(got))
	}
	if got[0].ID != "setup" {
		t.Errorf("got[0].ID = %q, want setup", got[0].ID)
	}
	if got[1].ID != "work" { // control
		t.Errorf("got[1].ID = %q, want work (control)", got[1].ID)
	}
	if got[2].ID != "work.iteration.1" {
		t.Errorf("got[2].ID = %q, want work.iteration.1", got[2].ID)
	}
	if got[3].ID != "cleanup" {
		t.Errorf("got[3].ID = %q, want cleanup", got[3].ID)
	}
}
