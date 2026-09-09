package main

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/doctor"
	"github.com/gastownhall/gascity/internal/events"
)

// drainAckFixedNow is the deterministic "current time" every test in this file reads
// its window from.
var drainAckFixedNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

// drainAckRateCheckWith builds a drainAckRateCheck over the fixed 30m AC
// window whose event reads are served from the supplied log instead of the
// city's events.jsonl. The fake applies the same predicates the real reader
// does, so tests exercise the check's windowing and rate arithmetic rather
// than a hand-stubbed answer.
func drainAckRateCheckWith(log []events.Event) *drainAckRateCheck {
	return &drainAckRateCheck{
		cityPath: "/city",
		window:   30 * time.Minute,
		now:      func() time.Time { return drainAckFixedNow },
		readEvents: func(_ string, filter events.Filter) ([]events.Event, error) {
			return events.ApplyFilter(log, filter), nil
		},
	}
}

// drainAck builds one drain-acked-with-assigned-work event at the given age
// relative to drainAckFixedNow.
func drainAckEvt(age time.Duration) events.Event {
	return events.Event{
		Type: events.SessionDrainAckedWithAssignedWork,
		Ts:   drainAckFixedNow.Add(-age),
	}
}

// otherEvent builds a non-matching event at the given age, used to establish
// event-log coverage without contributing to the measured rate.
func drainAckOtherEvt(age time.Duration) events.Event {
	return events.Event{Type: events.SessionWoke, Ts: drainAckFixedNow.Add(-age)}
}

func TestDrainAckRateCheck_ZeroOverFullyCoveredWindow(t *testing.T) {
	// Log reaches back 2h, so a 30m window is fully covered, and no
	// drain-ack events fall inside it: the AC-5 "rate is zero" reading.
	c := drainAckRateCheckWith([]events.Event{
		drainAckOtherEvt(2 * time.Hour),
		drainAckOtherEvt(time.Minute),
	})
	r := c.Run(nil)

	if r.Status != doctor.StatusOK {
		t.Fatalf("status = %v, want StatusOK (no drain-acks in window)", r.Status)
	}
	if r.Severity != doctor.SeverityAdvisory {
		t.Fatalf("severity = %v, want SeverityAdvisory (observability, never gates)", r.Severity)
	}
	if !strings.Contains(r.Message, "0.00/h") {
		t.Fatalf("message must report the rate as a number, got %q", r.Message)
	}
	if !strings.Contains(r.Message, "count=0") {
		t.Fatalf("message must report the raw count, got %q", r.Message)
	}
	if strings.Contains(r.Message, "partial") {
		t.Fatalf("a fully covered window must not be reported as partial, got %q", r.Message)
	}
}

func TestDrainAckRateCheck_NonZeroRateWarnsWithNumber(t *testing.T) {
	// 3 drain-acks inside a 30m (0.5h) fully covered window = 6.00/h.
	c := drainAckRateCheckWith([]events.Event{
		drainAckOtherEvt(4 * time.Hour),
		drainAckEvt(25 * time.Minute),
		drainAckEvt(10 * time.Minute),
		drainAckEvt(time.Minute),
	})
	r := c.Run(nil)

	if r.Status != doctor.StatusWarning {
		t.Fatalf("status = %v, want StatusWarning (drain-acks observed)", r.Status)
	}
	if r.Severity != doctor.SeverityAdvisory {
		t.Fatalf("severity = %v, want SeverityAdvisory", r.Severity)
	}
	if !strings.Contains(r.Message, "6.00/h") {
		t.Fatalf("message must report 6.00/h, got %q", r.Message)
	}
	if !strings.Contains(r.Message, "count=3") {
		t.Fatalf("message must report count=3, got %q", r.Message)
	}
	if len(r.Details) == 0 {
		t.Fatalf("a non-zero rate should carry diagnostic Details")
	}
}

func TestDrainAckRateCheck_ExcludesEventsOutsideWindow(t *testing.T) {
	// Only the 5m-old event is inside a 30m window; the 45m-old one is not.
	c := drainAckRateCheckWith([]events.Event{
		drainAckOtherEvt(3 * time.Hour),
		drainAckEvt(45 * time.Minute),
		drainAckEvt(5 * time.Minute),
	})
	r := c.Run(nil)

	if !strings.Contains(r.Message, "count=1") {
		t.Fatalf("only the in-window event may be counted, got %q", r.Message)
	}
	// 1 event over 0.5h = 2.00/h.
	if !strings.Contains(r.Message, "2.00/h") {
		t.Fatalf("message must report 2.00/h, got %q", r.Message)
	}
}

func TestDrainAckRateCheck_PartialCoverageIsDisclosed(t *testing.T) {
	// The log only reaches back 6m, so a 30m window is NOT fully observed.
	// A zero reading here is not 30 minutes of evidence and must say so,
	// otherwise AC-5 can be "shown" against a window that was never observed.
	c := drainAckRateCheckWith([]events.Event{
		drainAckOtherEvt(6 * time.Minute),
	})
	r := c.Run(nil)

	if r.Status != doctor.StatusOK {
		t.Fatalf("status = %v, want StatusOK (no drain-acks seen)", r.Status)
	}
	if !strings.Contains(r.Message, "partial") {
		t.Fatalf("partial coverage must be disclosed in the message, got %q", r.Message)
	}
	// The rate must be computed over the observed span (6m = 0.1h), not the
	// requested 30m, so it is not silently diluted by unobserved time.
	if !strings.Contains(r.Message, "observed=6m0s") {
		t.Fatalf("message must report the observed span, got %q", r.Message)
	}
}

func TestDrainAckRateCheck_PartialCoverageRateUsesObservedSpan(t *testing.T) {
	// 1 drain-ack, log covers only 6m (0.1h) of the requested 30m window.
	// The honest rate is 1/0.1h = 10.00/h, not 1/0.5h = 2.00/h.
	c := drainAckRateCheckWith([]events.Event{
		drainAckEvt(6 * time.Minute),
	})
	r := c.Run(nil)

	if !strings.Contains(r.Message, "10.00/h") {
		t.Fatalf("rate must be computed over the observed span, got %q", r.Message)
	}
}

func TestDrainAckRateCheck_EmptyLogReportsNoCoverage(t *testing.T) {
	c := drainAckRateCheckWith(nil)
	r := c.Run(nil)

	if r.Status != doctor.StatusOK {
		t.Fatalf("status = %v, want StatusOK (nothing observed is not a failure)", r.Status)
	}
	if r.Severity != doctor.SeverityAdvisory {
		t.Fatalf("severity = %v, want SeverityAdvisory", r.Severity)
	}
	if !strings.Contains(r.Message, "no events") {
		t.Fatalf("an empty log must say so rather than report a false 0.00/h, got %q", r.Message)
	}
}

func TestDrainAckRateCheck_ReadErrorIsAdvisoryNotGating(t *testing.T) {
	c := drainAckRateCheckWith(nil)
	c.readEvents = func(_ string, _ events.Filter) ([]events.Event, error) {
		return nil, errors.New("boom")
	}
	r := c.Run(nil)

	if r.Severity != doctor.SeverityAdvisory {
		t.Fatalf("severity = %v, want SeverityAdvisory (a read failure must never gate)", r.Severity)
	}
	if r.Status == doctor.StatusOK {
		t.Fatalf("a read failure must not be reported as a clean OK result")
	}
	if !strings.Contains(r.Message, "boom") {
		t.Fatalf("the underlying error must be surfaced, not swallowed, got %q", r.Message)
	}
}

func TestDrainAckRateCheck_DefaultsToACFloorWindow(t *testing.T) {
	// A zero window must fall back to the 30m AC-5 floor rather than
	// measuring over a zero-length span.
	c := newDrainAckRateCheck("/city", 0)
	if c.window != defaultDrainAckRateWindow {
		t.Fatalf("window = %v, want default %v", c.window, defaultDrainAckRateWindow)
	}
	if defaultDrainAckRateWindow < 30*time.Minute {
		t.Fatalf("default window %v is below the AC-5 floor of 30m", defaultDrainAckRateWindow)
	}
}

func TestDrainAckRateCheck_QueriesOnlyTheDrainAckEventType(t *testing.T) {
	var gotTypes []string
	c := drainAckRateCheckWith(nil)
	c.readEvents = func(_ string, filter events.Filter) ([]events.Event, error) {
		gotTypes = append(gotTypes, filter.Type)
		return nil, nil
	}
	c.Run(nil)

	var sawDrainAck bool
	for _, ty := range gotTypes {
		if ty == events.SessionDrainAckedWithAssignedWork {
			sawDrainAck = true
		}
	}
	if !sawDrainAck {
		t.Fatalf("check must filter on %s, saw filters %v",
			events.SessionDrainAckedWithAssignedWork, gotTypes)
	}
}

func TestDrainAckRateCheck_Metadata(t *testing.T) {
	c := newDrainAckRateCheck("/city", time.Hour)
	if c.Name() != "drain-ack-rate" {
		t.Fatalf("Name() = %q, want drain-ack-rate", c.Name())
	}
	if c.CanFix() {
		t.Fatalf("CanFix() = true, want false (pure observability)")
	}
	if err := c.Fix(nil); err != nil {
		t.Fatalf("Fix() = %v, want nil no-op", err)
	}
	if c.WarmupEligible() {
		t.Fatalf("WarmupEligible() = true; the check reads the whole event log and must not run in gc start's warm-up scan")
	}
}

func findDrainAckRateCheck(t *testing.T, checks []doctor.Check) *drainAckRateCheck {
	t.Helper()
	for _, c := range checks {
		if got, ok := c.(*drainAckRateCheck); ok {
			return got
		}
	}
	t.Fatal("drain-ack-rate check not found in buildDoctorChecks output")
	return nil
}

func TestDrainAckRateCheck_RegisteredByBuildDoctorChecks(t *testing.T) {
	checks := buildDoctorChecks(t.TempDir(), &config.City{}, nil, buildDoctorChecksOpts{
		SkipCityDoltCheck:    true,
		SkipManagedDoltCheck: true,
		DrainAckWindow:       45 * time.Minute,
	})
	c := findDrainAckRateCheck(t, checks)
	if c.window != 45*time.Minute {
		t.Fatalf("window = %v, want the caller-specified 45m", c.window)
	}
}

func TestDrainAckRateCheck_RegisteredWithDefaultWindow(t *testing.T) {
	// A zero opts window (every existing doDoctor call site) must still
	// register the check at the AC floor rather than a zero-length window.
	checks := buildDoctorChecks(t.TempDir(), &config.City{}, nil, buildDoctorChecksOpts{
		SkipCityDoltCheck:    true,
		SkipManagedDoltCheck: true,
	})
	c := findDrainAckRateCheck(t, checks)
	if c.window != defaultDrainAckRateWindow {
		t.Fatalf("window = %v, want default %v", c.window, defaultDrainAckRateWindow)
	}
}

func TestDoctorCmd_DrainAckWindowFlag(t *testing.T) {
	cmd := newDoctorCmd(io.Discard, io.Discard)
	f := cmd.Flags().Lookup("drain-ack-window")
	if f == nil {
		t.Fatal("gc doctor must expose --drain-ack-window so the window is caller-specified")
	}
	if f.DefValue != defaultDrainAckRateWindow.String() {
		t.Fatalf("--drain-ack-window default = %q, want %q", f.DefValue, defaultDrainAckRateWindow)
	}
	if err := cmd.Flags().Set("drain-ack-window", "2h"); err != nil {
		t.Fatalf("setting --drain-ack-window: %v", err)
	}
}
