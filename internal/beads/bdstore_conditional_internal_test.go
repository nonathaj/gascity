package beads

import (
	"errors"
	"fmt"
	"testing"
)

// TestClassifyConditionalWriteResult exhaustively exercises the pure classifier
// that maps a bd conditional-write invocation's (out, err) to the typed
// ConditionalWriter error surface. bd #4682 (the revision column + --if-revision)
// is unlanded, so the precondition/unsupported substrings are provisional; this
// table pins the DEFENSIBLE interim set (the //go:build integration conformance
// row against a #4682-capable bd is the authoritative guard). Classification is
// message-substring / body-code based, never exit-code based (BdStore has no
// exit-code path; every existing classifier matches on err.Error()).
func TestClassifyConditionalWriteResult(t *testing.T) {
	t.Run("success returns nil", func(t *testing.T) {
		if got := classifyConditionalWriteResult([]byte(`{}`), nil); got != nil {
			t.Fatalf("classify(nil err) = %v, want nil", got)
		}
	})

	t.Run("precondition from flat body", func(t *testing.T) {
		out := []byte(`{"error":"revision precondition failed","code":"precondition-failed","expected_revision":5,"current_revision":8}`)
		err := errors.New("exit status 1")
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 5, 8)
	})

	t.Run("precondition from data-wrapped envelope", func(t *testing.T) {
		out := []byte(`{"schema_version":6,"data":{"code":"precondition-failed","expected_revision":2,"current_revision":9}}`)
		err := errors.New("exit status 1")
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 2, 9)
	})

	t.Run("precondition body wrapped in log noise", func(t *testing.T) {
		out := []byte("bd: writing to dolt\n{\"code\":\"precondition-failed\",\"expected_revision\":1,\"current_revision\":4}\n")
		err := errors.New("exit status 1")
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 1, 4)
	})

	t.Run("precondition recovered from err string when stdout empty", func(t *testing.T) {
		// bd wrote the JSON envelope to stderr, so classifyBDExecResult folded it
		// into err.Error() and stdout is empty.
		err := errors.New(`exit status 1: {"code":"precondition-failed","expected_revision":3,"current_revision":7}`)
		assertPrecondition(t, classifyConditionalWriteResult(nil, err), 3, 7)
	})

	t.Run("precondition from revision fields without code", func(t *testing.T) {
		out := []byte(`{"error":"stale write","expected_revision":10,"current_revision":11}`)
		err := errors.New("exit status 1")
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 10, 11)
	})

	t.Run("precondition message with unparseable body is zero-valued with Raw", func(t *testing.T) {
		err := errors.New("exit status 1: revision precondition failed")
		got := classifyConditionalWriteResult(nil, err)
		var pfe *PreconditionFailedError
		if !errors.As(got, &pfe) {
			t.Fatalf("classify = %v, want *PreconditionFailedError", got)
		}
		if pfe.Expected != 0 || pfe.Current != 0 {
			t.Fatalf("unparseable body: Expected/Current = %d/%d, want 0/0", pfe.Expected, pfe.Current)
		}
		if pfe.Raw == "" {
			t.Fatal("unparseable precondition must set Raw for forensics")
		}
	})

	t.Run("precondition inferred from non-JSON message forms", func(t *testing.T) {
		// The message-fallback substrings (no parseable body): each must classify
		// as a zero-valued precondition so a caller still re-reads rather than
		// hard-failing. Guards the "revision mismatch" and hyphenated
		// "precondition-failed" message forms bd might emit outside a JSON envelope.
		for _, msg := range []string{
			"exit status 1: revision mismatch on ga-1",
			"exit status 1: error: precondition-failed for ga-1 (expected 3, got 5)",
		} {
			got := classifyConditionalWriteResult(nil, errors.New(msg))
			if !IsPreconditionFailed(got) {
				t.Fatalf("classify(%q) = %v, want *PreconditionFailedError", msg, got)
			}
		}
	})

	t.Run("unsupported from machine body code latches", func(t *testing.T) {
		out := []byte(`{"error":"conditional writes not supported","code":"conditional-write-unsupported"}`)
		err := errors.New("exit status 1")
		if got := classifyConditionalWriteResult(out, err); !IsConditionalWriteUnsupported(got) {
			t.Fatalf("classify = %v, want ErrConditionalWriteUnsupported", got)
		}
	})

	t.Run("unsupported from pre-4682 unknown-flag usage error latches", func(t *testing.T) {
		// The exact pflag/stdlib-flag phrasings, which put the flag token
		// immediately after the marker.
		for _, msg := range []string{
			"exit status 1: unknown flag: --if-revision",
			"exit status 1: unknown flag: -if-revision",
			"exit status 1: flag provided but not defined: -if-revision",
			"exit status 1: flag provided but not defined: --if-revision",
			"exit status 1: unknown flag '--if-revision'",
		} {
			err := errors.New(msg)
			if got := classifyConditionalWriteResult(nil, err); !IsConditionalWriteUnsupported(got) {
				t.Fatalf("classify(%q) = %v, want ErrConditionalWriteUnsupported", msg, got)
			}
		}
	})

	t.Run("unknown flag for a DIFFERENT flag must not latch", func(t *testing.T) {
		err := errors.New("exit status 1: unknown flag: --frobnicate")
		got := classifyConditionalWriteResult(nil, err)
		if IsConditionalWriteUnsupported(got) {
			t.Fatalf("classify = %v; a non-if-revision unknown flag must not latch the store incapable", got)
		}
		if IsPreconditionFailed(got) {
			t.Fatalf("classify = %v; unrelated unknown flag misread as precondition", got)
		}
		if got == nil || got.Error() != err.Error() {
			t.Fatalf("classify = %v, want the error surfaced as-is", got)
		}
	})

	t.Run("capable bd usage-echo listing --if-revision must not latch on an unrelated flag error", func(t *testing.T) {
		// A CAPABLE bd, given some other unknown flag, echoes usage that LISTS
		// --if-revision in the flags block. classifyBDExecResult folds the whole
		// stderr into err.Error(), so a floating "contains if-revision" latch would
		// silently degrade every future fenced write on a perfectly capable bd.
		err := errors.New("exit status 1: unknown flag: --reason-code\n" +
			"Usage:\n  bd update [flags]\n\nFlags:\n" +
			"      --if-revision int   apply only when the bead is at this revision\n" +
			"      --json              emit JSON\n")
		got := classifyConditionalWriteResult(nil, err)
		if IsConditionalWriteUnsupported(got) {
			t.Fatalf("classify = %v; a capable bd's usage echo must NOT latch it incapable", got)
		}
		if got == nil || got.Error() != err.Error() {
			t.Fatalf("classify = %v, want the unrelated flag error surfaced as-is", got)
		}
	})

	t.Run("gate refusal from other body code never latches", func(t *testing.T) {
		out := []byte(`{"error":"close authority required","code":"close-authority-required"}`)
		err := errors.New("exit status 1")
		got := classifyConditionalWriteResult(out, err)
		var gre *GateRefusalError
		if !errors.As(got, &gre) {
			t.Fatalf("classify = %v, want *GateRefusalError", got)
		}
		if gre.Code != "close-authority-required" {
			t.Fatalf("GateRefusalError.Code = %q, want %q", gre.Code, "close-authority-required")
		}
		if IsConditionalWriteUnsupported(got) {
			t.Fatal("a policy gate refusal must NOT latch the store incapable")
		}
	})

	t.Run("gate refusal carrying an informational revision is NOT a precondition", func(t *testing.T) {
		// A close-authority refusal may attach the current revision for context.
		// Field-presence keying would misread it as a precondition and spin the
		// CAS-emulation retry loop against a permanent refusal. The machine code
		// must dominate the fields.
		out := []byte(`{"error":"close denied: not lease holder","code":"close-authority","current_revision":7}`)
		err := errors.New("exit status 1")
		got := classifyConditionalWriteResult(out, err)
		if IsPreconditionFailed(got) {
			t.Fatalf("classify = %v; a coded refusal with an informational revision must not read as a precondition", got)
		}
		var gre *GateRefusalError
		if !errors.As(got, &gre) || gre.Code != "close-authority" {
			t.Fatalf("classify = %v, want *GateRefusalError{Code:\"close-authority\"}", got)
		}
	})

	t.Run("ambiguous error outranks a machine code (may have committed)", func(t *testing.T) {
		// A body code must not convert a maybe-committed connection failure into a
		// definitive did-not-commit gate refusal.
		out := []byte(`{"error":"driver: bad connection","code":"storage"}`)
		err := errors.New("exit status 1: driver: bad connection")
		got := classifyConditionalWriteResult(out, err)
		if IsGateRefusal(got) {
			t.Fatalf("classify = %v; an ambiguous (maybe-committed) write must not be reported as a gate refusal", got)
		}
		if got == nil || got.Error() != err.Error() {
			t.Fatalf("classify = %v, want the ambiguous error surfaced as-is", got)
		}
	})

	t.Run("coded gate refusal whose message says 'not found' is NOT swallowed as ErrNotFound", func(t *testing.T) {
		// A policy refusal may mention "not found" in its human text ("lease not
		// found for holder ..."). The machine code must win over the loose
		// not-found substring, or a permanent refusal becomes a silent idempotent
		// success for delete/close callers.
		out := []byte(`{"error":"lease not found for holder agent-7","code":"close-authority-required"}`)
		err := errors.New("exit status 1: lease not found for holder agent-7")
		got := classifyConditionalWriteResult(out, err)
		if errors.Is(got, ErrNotFound) {
			t.Fatalf("classify = %v; a coded gate refusal must not be swallowed as ErrNotFound", got)
		}
		var gre *GateRefusalError
		if !errors.As(got, &gre) || gre.Code != "close-authority-required" {
			t.Fatalf("classify = %v, want *GateRefusalError{Code:\"close-authority-required\"}", got)
		}
	})

	t.Run("code-less not-found maps to ErrNotFound", func(t *testing.T) {
		err := errors.New("exit status 1: no issues found matching the provided IDs")
		if got := classifyConditionalWriteResult(nil, err); !errors.Is(got, ErrNotFound) {
			t.Fatalf("classify = %v, want ErrNotFound", got)
		}
	})

	t.Run("precondition body on stderr wins over an incidental stdout envelope", func(t *testing.T) {
		// bd may split streams: an incidental message-only JSON on stdout, the real
		// coded precondition body folded into err.Error() from stderr. The source
		// carrying a discriminator must win.
		out := []byte(`{"error":"progress: 1 of 2 committed"}`)
		err := errors.New(`exit status 1: {"code":"precondition-failed","expected_revision":3,"current_revision":9}`)
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 3, 9)
	})

	t.Run("precondition body with trailing log noise still parses", func(t *testing.T) {
		out := []byte("{\"code\":\"precondition-failed\",\"expected_revision\":6,\"current_revision\":6}\nWARN: dolt reconnected\n")
		err := errors.New("exit status 1")
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 6, 6)
	})

	t.Run("precondition body behind a bracketed log prefix parses", func(t *testing.T) {
		// extractJSON stops at the first '{' OR '[': a "[WARN]" prefix would make a
		// naive parse reject the object. The multi-object scan must skip it.
		out := []byte("[WARN] dolt reconnect\n{\"code\":\"precondition-failed\",\"expected_revision\":3,\"current_revision\":9}")
		err := errors.New("exit status 1")
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 3, 9)
	})

	t.Run("precondition body after a leading JSON log line parses", func(t *testing.T) {
		// A JSON log line precedes the real envelope; the first-object-only parse
		// would read the log line (no discriminator) and miss the body.
		out := []byte("{\"level\":\"info\",\"msg\":\"connecting\"}\n{\"code\":\"precondition-failed\",\"expected_revision\":4,\"current_revision\":5}")
		err := errors.New("exit status 1")
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 4, 5)
	})

	t.Run("two-source: winning body carries only a code (no revisions)", func(t *testing.T) {
		// The stdout envelope is message-only (no discriminator); the real coded
		// body rides err.Error(). The discriminator-preferring parse must pick it,
		// exercising the Code arm of hasDiscriminator alone.
		out := []byte(`{"error":"progress: 1 of 2 committed"}`)
		err := errors.New(`exit status 1: {"code":"conditional-write-unsupported"}`)
		if got := classifyConditionalWriteResult(out, err); !IsConditionalWriteUnsupported(got) {
			t.Fatalf("classify = %v, want ErrConditionalWriteUnsupported from the err-string body", got)
		}
	})

	t.Run("two-source: winning body carries only revision fields (no code)", func(t *testing.T) {
		// Exercises the revision-field arm of hasDiscriminator alone.
		out := []byte(`{"error":"progress: 1 of 2 committed"}`)
		err := errors.New(`exit status 1: {"expected_revision":10,"current_revision":11}`)
		assertPrecondition(t, classifyConditionalWriteResult(out, err), 10, 11)
	})

	t.Run("ambiguous connection class surfaces as-is", func(t *testing.T) {
		for _, detail := range []string{
			"i/o timeout", "invalid connection", "bad connection",
			"connection reset", "broken pipe", "timed out after 5s", "deadline exceeded",
		} {
			err := fmt.Errorf("exit status 1: %s", detail)
			got := classifyConditionalWriteResult(nil, err)
			if got == nil || got.Error() != err.Error() {
				t.Fatalf("classify(%q) = %v, want the ambiguous error surfaced as-is", detail, got)
			}
			if IsPreconditionFailed(got) || IsConditionalWriteUnsupported(got) {
				t.Fatalf("classify(%q) = %v, ambiguous error misclassified", detail, got)
			}
		}
	})

	t.Run("ambiguous outranks a code-less not-found when both phrases appear", func(t *testing.T) {
		// A maybe-committed connection failure whose text also contains "no issues
		// found" must surface as-is, never as a definitive (idempotent-success)
		// ErrNotFound — the write may have landed.
		err := errors.New("exit status 1: connection reset by peer; no issues found in retry")
		got := classifyConditionalWriteResult(nil, err)
		if errors.Is(got, ErrNotFound) {
			t.Fatalf("classify = %v; an ambiguous (maybe-committed) write must not be reported as not-found", got)
		}
		if got == nil || got.Error() != err.Error() {
			t.Fatalf("classify = %v, want the ambiguous error surfaced as-is", got)
		}
	})

	t.Run("generic error surfaces as-is", func(t *testing.T) {
		err := errors.New("exit status 1: dolt merge conflict on issues")
		got := classifyConditionalWriteResult(nil, err)
		if got == nil || got.Error() != err.Error() {
			t.Fatalf("classify = %v, want the error surfaced as-is", got)
		}
		if IsPreconditionFailed(got) || IsConditionalWriteUnsupported(got) || IsGateRefusal(got) {
			t.Fatalf("classify = %v, generic error misclassified", got)
		}
	})
}

func assertPrecondition(t *testing.T, got error, wantExpected, wantCurrent int64) {
	t.Helper()
	var pfe *PreconditionFailedError
	if !errors.As(got, &pfe) {
		t.Fatalf("classify = %v, want *PreconditionFailedError", got)
	}
	if pfe.Expected != wantExpected {
		t.Fatalf("PreconditionFailedError.Expected = %d, want %d", pfe.Expected, wantExpected)
	}
	if pfe.Current != wantCurrent {
		t.Fatalf("PreconditionFailedError.Current = %d, want %d", pfe.Current, wantCurrent)
	}
}

// TestConditionalWritesCapableProbe covers the lazy four-verb capability probe
// and the runtime unsupported latch that is authoritative over it.
func TestConditionalWritesCapableProbe(t *testing.T) {
	// A --help body advertising --if-revision for the given verb.
	capableHelp := func(verb string) []byte {
		return []byte("Usage:\n  bd " + verb + " [flags]\n\nFlags:\n  --if-revision int   apply only at this revision\n")
	}
	incapableHelp := func(verb string) []byte {
		return []byte("Usage:\n  bd " + verb + " [flags]\n\nFlags:\n  --json   emit JSON\n")
	}

	t.Run("all four verbs advertise the flag -> capable, probed once", func(t *testing.T) {
		var calls int
		seen := map[string]int{}
		s := NewBdStore("/city", func(_, _ string, args ...string) ([]byte, error) {
			calls++
			verb := args[0]
			seen[verb]++
			return capableHelp(verb), nil
		})
		ok, err := s.conditionalWritesCapable()
		if err != nil || !ok {
			t.Fatalf("conditionalWritesCapable = (%v, %v), want (true, nil)", ok, err)
		}
		if calls != 4 {
			t.Fatalf("probe ran %d subprocesses, want 4 (one per verb)", calls)
		}
		for _, verb := range []string{"update", "close", "assign", "delete"} {
			if seen[verb] != 1 {
				t.Fatalf("verb %q probed %d times, want 1", verb, seen[verb])
			}
		}
		// Memoized: a second call issues no new subprocesses.
		if ok2, _ := s.conditionalWritesCapable(); !ok2 {
			t.Fatal("second call lost the capable verdict")
		}
		if calls != 4 {
			t.Fatalf("probe re-ran subprocesses on the memoized path: %d calls, want 4", calls)
		}
	})

	t.Run("a later verb missing the flag -> incapable", func(t *testing.T) {
		var calls int
		s := NewBdStore("/city", func(_, _ string, args ...string) ([]byte, error) {
			calls++
			verb := args[0]
			if verb == "delete" {
				return incapableHelp(verb), nil
			}
			return capableHelp(verb), nil
		})
		ok, err := s.conditionalWritesCapable()
		if err != nil || ok {
			t.Fatalf("conditionalWritesCapable = (%v, %v), want (false, nil)", ok, err)
		}
		if calls != 4 {
			t.Fatalf("probe ran %d subprocesses, want 4 (delete is the 4th)", calls)
		}
		// The incapable verdict must also be memoized: a second call re-probes
		// nothing, or a mid-process bd swap could flip the verdict.
		if ok2, _ := s.conditionalWritesCapable(); ok2 {
			t.Fatal("second call flipped the incapable verdict")
		}
		if calls != 4 {
			t.Fatalf("incapable verdict not memoized: %d calls after a second query, want 4", calls)
		}
	})

	t.Run("first verb missing the flag short-circuits", func(t *testing.T) {
		var calls int
		s := NewBdStore("/city", func(_, _ string, args ...string) ([]byte, error) {
			calls++
			return incapableHelp(args[0]), nil
		})
		if ok, _ := s.conditionalWritesCapable(); ok {
			t.Fatal("want incapable when the first verb lacks --if-revision")
		}
		if calls != 1 {
			t.Fatalf("probe ran %d subprocesses, want 1 (short-circuit on first miss)", calls)
		}
	})

	t.Run("help subprocess error -> incapable", func(t *testing.T) {
		s := NewBdStore("/city", func(_, _ string, _ ...string) ([]byte, error) {
			return nil, errors.New("exec: bd not found")
		})
		if ok, err := s.conditionalWritesCapable(); ok || err != nil {
			t.Fatalf("conditionalWritesCapable = (%v, %v), want (false, nil) on probe error", ok, err)
		}
	})

	t.Run("latch is authoritative over a capable probe", func(t *testing.T) {
		var calls int
		s := NewBdStore("/city", func(_, _ string, args ...string) ([]byte, error) {
			calls++
			return capableHelp(args[0]), nil
		})
		if ok, _ := s.conditionalWritesCapable(); !ok {
			t.Fatal("precondition: probe should report capable")
		}
		s.markConditionalWritesUnsupported()
		if ok, _ := s.conditionalWritesCapable(); ok {
			t.Fatal("latch must override a capable probe verdict")
		}
	})

	t.Run("latch before first probe returns incapable without probing", func(t *testing.T) {
		var calls int
		s := NewBdStore("/city", func(_, _ string, args ...string) ([]byte, error) {
			calls++
			return capableHelp(args[0]), nil
		})
		s.markConditionalWritesUnsupported()
		if ok, _ := s.conditionalWritesCapable(); ok {
			t.Fatal("a latched store must report incapable")
		}
		if calls != 0 {
			t.Fatalf("latched store ran %d probe subprocesses, want 0", calls)
		}
	})
}
