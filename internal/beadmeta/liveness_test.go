package beadmeta

import (
	"strings"
	"testing"
)

// TestIsLiveStatusRejectsOnlyClosed pins the liveness contract shared by the
// claim admission doors and the routed work/demand queries: a row is dead when
// and only when the store says it is closed.
func TestIsLiveStatusRejectsOnlyClosed(t *testing.T) {
	dead := []string{StatusClosed, "CLOSED", "Closed", "  closed  ", "\tclosed\n"}
	for _, status := range dead {
		if IsLiveStatus(status) {
			t.Errorf("IsLiveStatus(%q) = true, want false (closed rows are never live)", status)
		}
	}
}

// TestIsLiveStatusAcceptsLiveRows covers the statuses that must keep flowing
// through both consumers, including in_progress, which the adoption door
// legitimately serves as reason=existing_assignment.
func TestIsLiveStatusAcceptsLiveRows(t *testing.T) {
	live := []string{StatusOpen, StatusInProgress, "OPEN", " in_progress ", "blocked", "deferred"}
	for _, status := range live {
		if !IsLiveStatus(status) {
			t.Errorf("IsLiveStatus(%q) = false, want true", status)
		}
	}
}

// TestIsLiveStatusTreatsAbsentStatusAsLive documents the deliberate fail-open
// choice on the *unknown* axis: a row that carries no status field at all is
// not evidence of closure, so it stays visible. Dropping it would starve every
// consumer against any store or fixture that omits the field.
func TestIsLiveStatusTreatsAbsentStatusAsLive(t *testing.T) {
	if !IsLiveStatus("") {
		t.Error("IsLiveStatus(\"\") = false, want true (absent status is not evidence of closure)")
	}
}

// TestLivenessJQSelectRendersFromTheSameConstant guards the one-definition rule
// from the shell side: the jq fragment the query generators embed must be
// derived from StatusClosed, not from a second hand-written spelling.
func TestLivenessJQSelectRendersFromTheSameConstant(t *testing.T) {
	got := LivenessJQSelect()
	if !strings.Contains(got, StatusClosed) {
		t.Errorf("LivenessJQSelect() = %q, want it to embed StatusClosed %q", got, StatusClosed)
	}
	if !strings.HasPrefix(got, "select(") {
		t.Errorf("LivenessJQSelect() = %q, want a jq select(...) expression", got)
	}
}
