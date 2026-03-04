package peek

import (
	"testing"
	"time"

	"github.com/julianknutsen/gascity/internal/agent"
)

// fakePeeker is a test double that returns canned output.
type fakePeeker struct {
	output string
	err    error
}

func (f *fakePeeker) Peek(_ int) (string, error) {
	return f.output, f.err
}

func TestObserverEmitsOnChange(t *testing.T) {
	fp := &fakePeeker{output: "hello"}
	obs := New("test-agent", fp, 10)
	defer obs.Close() //nolint:errcheck // test cleanup

	select {
	case ev := <-obs.Events():
		if ev.Type != agent.EventOutput {
			t.Fatalf("got type %v, want EventOutput", ev.Type)
		}
		if ev.Agent != "test-agent" {
			t.Fatalf("got agent %q, want %q", ev.Agent, "test-agent")
		}
		if ev.Data != "hello" {
			t.Fatalf("got data %q, want %q", ev.Data, "hello")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestObserverSuppressesDuplicates(t *testing.T) {
	fp := &fakePeeker{output: "same"}
	obs := New("test-agent", fp, 10)
	defer obs.Close() //nolint:errcheck // test cleanup

	// First event should arrive.
	select {
	case <-obs.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for first event")
	}

	// No second event since output hasn't changed.
	select {
	case ev := <-obs.Events():
		t.Fatalf("unexpected duplicate event: %+v", ev)
	case <-time.After(800 * time.Millisecond):
		// Expected — no duplicate.
	}
}

func TestObserverEmitsOnSubsequentChange(t *testing.T) {
	fp := &fakePeeker{output: "first"}
	obs := New("test-agent", fp, 10)
	defer obs.Close() //nolint:errcheck // test cleanup

	// Drain first event.
	select {
	case <-obs.Events():
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for first event")
	}

	// Change output.
	fp.output = "second"

	select {
	case ev := <-obs.Events():
		if ev.Data != "second" {
			t.Fatalf("got data %q, want %q", ev.Data, "second")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for second event")
	}
}

func TestObserverCloseStopsChannel(t *testing.T) {
	fp := &fakePeeker{output: "hello"}
	obs := New("test-agent", fp, 10)
	obs.Close() //nolint:errcheck // test cleanup

	// Channel should close eventually.
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case _, ok := <-obs.Events():
			if !ok {
				return // channel closed — success
			}
			// drain any buffered events
		case <-timer.C:
			t.Fatal("timeout waiting for channel close")
		}
	}
}

func TestObserverDefaultLines(t *testing.T) {
	fp := &fakePeeker{output: "x"}
	obs := New("a", fp, 0) // 0 → default 50
	defer obs.Close()      //nolint:errcheck // test cleanup

	if obs.lines != 50 {
		t.Fatalf("got lines %d, want 50", obs.lines)
	}
}
