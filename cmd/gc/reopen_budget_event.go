package main

import (
	"strconv"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beadmeta"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/events"
)

// emitReopenBudgetExhaustedEvents records one alarm-level
// bead.reopen_budget_exhausted event for each bead whose reopen this tick spent
// its per-window budget. It is the escalation peer of
// emitDeadAssigneeReopenedEvents: that one reports every routine repair, this
// one reports that the repair has stopped working and the sweep is standing
// down on that bead until its window elapses.
//
// Only releases flagged BudgetExhausted alarm, and the sweep sets that flag on
// exactly the release that reaches the limit, so a bead wedged for hours raises
// one alarm per window rather than one per tick. Like its peer this never
// mutates a bead — the reopen already happened, and the refusals that follow are
// decided by the budget, not by this event.
//
// released carries the index into assignedWorkBeads (the pre-reopen snapshot) so
// the dead assignee and route can be read off the bead as it looked when it was
// reopened. A stale/out-of-range index still alarms — the budget fact is the
// point of the event — but with the identity fields left empty rather than
// fabricated.
func emitReopenBudgetExhaustedEvents(rec events.Recorder, assignedWorkBeads []beads.Bead, released []releasedPoolAssignment, now time.Time) {
	if rec == nil || len(released) == 0 {
		return
	}
	windowSeconds := int(reopenBudgetWindow.Seconds())
	for _, r := range released {
		if !r.BudgetExhausted {
			continue
		}
		deadAssignee := ""
		routedTo := ""
		if r.Index >= 0 && r.Index < len(assignedWorkBeads) && assignedWorkBeads[r.Index].ID == r.ID {
			wb := assignedWorkBeads[r.Index]
			deadAssignee = strings.TrimSpace(wb.Assignee)
			routedTo = strings.TrimSpace(wb.Metadata[beadmeta.RoutedToMetadataKey])
		}
		rec.Record(events.Event{
			Type:    events.BeadReopenBudgetExhausted,
			Ts:      now.UTC(),
			Actor:   "gc",
			Subject: r.ID,
			Message: formatReopenBudgetExhaustedMessage(r.ID, deadAssignee, routedTo, r.ReopenCount),
			Payload: api.BeadReopenBudgetExhaustedPayloadJSON(r.ID, deadAssignee, routedTo, r.ReopenCount, reopenBudgetLimit, windowSeconds),
		})
	}
}

// formatReopenBudgetExhaustedMessage renders the operator-facing text for a
// bead.reopen_budget_exhausted event. It says what stopped and what to look at,
// because the whole point of the alarm is that nobody was reading the reopens.
func formatReopenBudgetExhaustedMessage(beadID, deadAssignee, routedTo string, count int) string {
	assignee := deadAssignee
	if assignee == "" {
		assignee = "<unknown>"
	}
	route := routedTo
	if route == "" {
		route = "<unrouted>"
	}
	return "reopen budget exhausted for " + beadID + ": reopened " + strconv.Itoa(count) +
		" times in " + reopenBudgetWindow.String() + " (limit " + strconv.Itoa(reopenBudgetLimit) +
		"), last assigned to dead session " + assignee + " (route " + route +
		"); no longer reopening it this window — its worker keeps dying, investigate " + route
}
