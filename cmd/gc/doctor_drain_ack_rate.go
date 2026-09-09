package main

import (
	"fmt"
	"time"

	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/doctor"
	"github.com/gastownhall/gascity/internal/events"
)

// drainAckRateCheck reports how often sessions acknowledge a drain while work
// was still assigned to them — the `session.drain_acked_with_assigned_work`
// event — as a rate over a caller-specified window.
//
// The event marks a session that was told to drain and complied even though a
// bead was still on its hook: the work is stranded and has to be recovered by
// another observer. A single occurrence is a curiosity; a sustained rate is the
// claim/drain loop, where sessions are handed work and drained faster than they
// can run it. That distinction is only visible as a rate, and until this check
// existed the event could be read one-by-one out of the log but never measured,
// so "the loop is fixed" could only be asserted, not shown.
//
// The rate is computed over the span the event log actually covers, not the
// span the caller asked for. Those differ whenever the log is younger than the
// window (a fresh city, a rotated-away history), and conflating them turns
// "nothing happened in 6 minutes" into a false claim of a clean 30-minute
// window. When coverage is short the result says so and divides by the observed
// span, so the reported number is never diluted by time nobody watched.
//
// Pure observability (SeverityAdvisory): it reads the append-only event log and
// never mutates anything or gates a run.
type drainAckRateCheck struct {
	// cityPath is the city root whose event log is measured.
	cityPath string
	// window is the caller-specified measurement window.
	window time.Duration
	// now returns the current time. Injectable for deterministic tests.
	now func() time.Time
	// readEvents reads matching events from the city event log, transparently
	// walking rotation archives. Injectable for tests.
	readEvents func(path string, filter events.Filter) ([]events.Event, error)
}

// defaultDrainAckRateWindow is the window used when the caller does not
// specify one. It matches the 30-minute floor the acceptance criterion for
// this measurement requires, so the default reading is always admissible
// evidence.
const defaultDrainAckRateWindow = 30 * time.Minute

// newDrainAckRateCheck builds the drain-ack rate check for a city, measuring
// over window. A non-positive window falls back to defaultDrainAckRateWindow.
func newDrainAckRateCheck(cityPath string, window time.Duration) *drainAckRateCheck {
	if window <= 0 {
		window = defaultDrainAckRateWindow
	}
	return &drainAckRateCheck{
		cityPath:   cityPath,
		window:     window,
		now:        time.Now,
		readEvents: events.ReadFiltered,
	}
}

func (c *drainAckRateCheck) Name() string                     { return "drain-ack-rate" }
func (c *drainAckRateCheck) CanFix() bool                     { return false }
func (c *drainAckRateCheck) Fix(_ *doctor.CheckContext) error { return nil }

// WarmupEligible reports false: the check walks the whole event log and its
// answer is a trend, not a start-up precondition.
func (c *drainAckRateCheck) WarmupEligible() bool { return false }

// eventLogPath returns the city's append-only event log.
func (c *drainAckRateCheck) eventLogPath() string {
	return citylayout.RuntimePath(c.cityPath, "events.jsonl")
}

func (c *drainAckRateCheck) Run(_ *doctor.CheckContext) *doctor.CheckResult {
	res := &doctor.CheckResult{Name: c.Name(), Severity: doctor.SeverityAdvisory}

	now := c.now()
	windowStart := now.Add(-c.window)
	path := c.eventLogPath()

	matches, err := c.readEvents(path, events.Filter{
		Type:  events.SessionDrainAckedWithAssignedWork,
		Since: windowStart,
	})
	if err != nil {
		res.Status = doctor.StatusWarning
		res.Message = fmt.Sprintf("drain-ack rate: reading the event log failed: %v", err)
		return res
	}

	// The oldest event of any type bounds how far back the log can testify.
	// Without it a zero count is indistinguishable from an unwatched window.
	oldest, err := c.readEvents(path, events.Filter{Limit: 1})
	if err != nil {
		res.Status = doctor.StatusWarning
		res.Message = fmt.Sprintf("drain-ack rate: reading event-log coverage failed: %v", err)
		return res
	}

	count := len(matches)
	if len(oldest) == 0 {
		res.Status = doctor.StatusOK
		res.Message = fmt.Sprintf("drain-ack rate: no events in the city event log — nothing observed (window=%s)", c.window)
		return res
	}

	// Measure over the intersection of the requested window and the span the
	// log actually covers.
	coverageStart := windowStart
	if logStart := oldest[0].Ts; logStart.After(coverageStart) {
		coverageStart = logStart
	}
	observed := now.Sub(coverageStart)
	if observed <= 0 {
		res.Status = doctor.StatusOK
		res.Message = fmt.Sprintf("drain-ack rate: event log covers no measurable span inside window=%s — nothing observed", c.window)
		return res
	}

	perHour := float64(count) / observed.Hours()
	partial := observed < c.window

	summary := fmt.Sprintf("drain-ack rate: %.2f/h (count=%d, window=%s, observed=%s)",
		perHour, count, c.window, observed.Round(time.Second))
	if partial {
		summary += " — partial coverage: the event log does not reach back the full window, so this is not evidence for the whole window"
	}

	if count > 0 {
		res.Status = doctor.StatusWarning
		res.Message = summary
		res.Details = []string{
			"Sessions acknowledged a drain while a bead was still assigned to them; that work was",
			"stranded and had to be recovered by another observer. A sustained rate is the claim/drain",
			"loop: sessions are handed work and drained faster than they can run it.",
			fmt.Sprintf("List the underlying events: gc events --type %s --since %s",
				events.SessionDrainAckedWithAssignedWork, c.window),
			"Each event payload carries the session, bead, template, and bead status at drain time.",
		}
		return res
	}

	res.Status = doctor.StatusOK
	res.Message = summary
	return res
}
