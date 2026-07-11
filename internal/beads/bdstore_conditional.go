package beads

import (
	"bytes"
	"encoding/json"
	"strings"
)

// This file holds BdStore's ConditionalWriter machinery that has no exit-code to
// key on: bd surfaces every failure as exit 1 with a JSON error envelope, so both
// the capability probe and the result classifier are message/body based, mirroring
// the existing isBdTransientWriteError / isBdNotFound / isBdAmbiguousWriteError
// classifiers. The *IfMatch verbs and the metadata-CAS emulation that consume this
// machinery land in a later phase.

// conditionalWriteProbeVerbs are the bd subcommands whose --help output must all
// advertise --if-revision for the store to be treated as CAS-capable. All four
// are probed because consumers issue update/close/assign/delete conditional
// writes and a dev bd mid-merge of the revision-CAS feature can support one verb
// but not another; a single-verb probe would report capable and then eat runtime
// refusals with the probe still showing clean.
var conditionalWriteProbeVerbs = []string{"update", "close", "assign", "delete"}

const conditionalWriteFlag = "--if-revision"

// conditionalWritesCapable reports whether the bd behind this store parses
// --if-revision on every conditional-write verb. The verdict is memoized per
// store instance (mirroring bdReadyProjectionEnabled): the probe fires lazily on
// the first conditional write, never at construction, so short-lived read-only
// CLI paths (gc hook) pay no four-subprocess tax. A probe error or any missing
// flag degrades to incapable — a fail-closed veto, never an unconditional write.
//
// The runtime unsupported latch (markConditionalWritesUnsupported) is
// authoritative over the probe in both directions of skew: a bd downgraded in
// place, or a drifted PATH, stops issuing fenced writes for the process lifetime
// rather than silently degrading them. Nothing is persisted; a restart re-probes
// the live bd, matching the "no status files — query live state" rule.
func (s *BdStore) conditionalWritesCapable() (bool, error) {
	s.condWriteMu.Lock()
	defer s.condWriteMu.Unlock()
	if s.condWriteLatched {
		return false, nil
	}
	if s.condWriteProbed {
		return s.condWriteCapable, nil
	}
	for _, verb := range conditionalWriteProbeVerbs {
		out, err := s.runner(s.dir, "bd", verb, "--help")
		if err != nil || !bytes.Contains(out, []byte(conditionalWriteFlag)) {
			s.condWriteProbed, s.condWriteCapable = true, false
			return false, nil
		}
	}
	s.condWriteProbed, s.condWriteCapable = true, true
	return true, nil
}

// markConditionalWritesUnsupported latches this store instance incapable after a
// real conditional write returned ErrConditionalWriteUnsupported. Because the
// latch is authoritative over the probe, one machine-confirmed unsupported
// response halts every subsequent fenced write on this store — the capability
// veto — instead of letting a stale "capable" probe verdict keep issuing writes
// bd can no longer honor.
func (s *BdStore) markConditionalWritesUnsupported() {
	s.condWriteMu.Lock()
	defer s.condWriteMu.Unlock()
	s.condWriteLatched = true
}

// Machine body codes bd emits (or, per beads #4682, will emit) for conditional
// writes. The codes are provisional until #4682 lands; the //go:build integration
// conformance row against a #4682-capable bd is the authoritative guard.
const (
	bdConditionalCodePreconditionFailed = "precondition-failed"
	bdConditionalCodeUnsupported        = "conditional-write-unsupported"
)

// bdConditionalErrorBody is the machine JSON bd attaches to a failed conditional
// write. bd's error envelope is either flat ({"error","hint","schema_version",
// ...}) or wrapped ({"schema_version","data":{...}}); decodeBdConditionalBody
// handles both. The revision fields are pointers so an absent field (nil) is
// distinguishable from a legitimate zero revision.
type bdConditionalErrorBody struct {
	Error            string `json:"error"`
	Code             string `json:"code"`
	ExpectedRevision *int64 `json:"expected_revision"`
	CurrentRevision  *int64 `json:"current_revision"`
}

// hasDiscriminator reports whether the body carries a signal the classifier keys
// on — a machine code or a revision field. Bodies without one are unhelpful
// human-message-only envelopes.
func (b bdConditionalErrorBody) hasDiscriminator() bool {
	return b.Code != "" || b.ExpectedRevision != nil || b.CurrentRevision != nil
}

// parseBdConditionalErrorBody recovers bd's structured error body from the
// command stdout or, when bd wrote the JSON envelope to stderr (which
// classifyBDExecResult folds into err.Error()), from the error string. bd splits
// streams inconsistently (bdStdoutErrorDetail embeds only the human "error" text
// into err.Error(), while the machine fields ride whichever stream carried the
// JSON), so both sources are scanned and the first object carrying a real
// discriminator (code / revision fields) wins over incidental message-only or
// log envelopes. ok is false when no JSON object is recoverable from either
// source.
func parseBdConditionalErrorBody(out []byte, err error) (bdConditionalErrorBody, bool) {
	sources := [][]byte{out}
	if err != nil {
		sources = append(sources, []byte(err.Error()))
	}
	var (
		fallback bdConditionalErrorBody
		haveAny  bool
	)
	for _, src := range sources {
		for _, body := range decodeBdConditionalBodies(src) {
			if body.hasDiscriminator() {
				return body, true
			}
			if !haveAny {
				fallback, haveAny = body, true
			}
		}
	}
	return fallback, haveAny
}

// decodeBdConditionalBodies scans src for every JSON object it contains, in
// order, unwrapping the {"data":{...}} envelope form. It is deliberately more
// tolerant than extractJSON, which stops at the first '{' OR '[': bd prefixes and
// interleaves its error envelope with log lines that are either bracketed
// ("[WARN] dolt reconnect") or JSON ({"level":"info",...}), and either would hide
// a coded precondition body from a single first-brace parse. Each candidate is
// decoded with json.Decoder so trailing bytes after one object don't reject it;
// callers pick the object carrying a discriminator.
func decodeBdConditionalBodies(src []byte) []bdConditionalErrorBody {
	var bodies []bdConditionalErrorBody
	for i := 0; i < len(src); {
		brace := bytes.IndexByte(src[i:], '{')
		if brace < 0 {
			break
		}
		i += brace
		dec := json.NewDecoder(bytes.NewReader(src[i:]))
		var env struct {
			Data *bdConditionalErrorBody `json:"data"`
			bdConditionalErrorBody
		}
		if dec.Decode(&env) != nil {
			i++ // not a valid object at this '{'; step past it and keep scanning
			continue
		}
		if env.Data != nil {
			bodies = append(bodies, *env.Data)
		} else {
			bodies = append(bodies, env.bdConditionalErrorBody)
		}
		i += int(dec.InputOffset())
	}
	return bodies
}

// classifyConditionalWriteResult maps a bd conditional-write invocation's result
// to the typed ConditionalWriter error surface. It is pure over exactly what the
// runner returns — (out, err) — and message/body based, not exit-code based:
// BdStore has no exit-code path and bd exits 1 for every error while writing a
// JSON envelope, so the "exit 9 / exit 13" split in the design doc is a misnomer
// for this codebase. The signals here are the machine body code and message
// substrings.
//
// The mapping, in priority order:
//   - nil on success.
//   - A machine body code is the AUTHORITATIVE discriminator when present: the
//     precondition code yields *PreconditionFailedError, the unsupported code
//     yields the latching ErrConditionalWriteUnsupported. A code never coexists
//     with a different class, so informational revision fields on, say, a
//     close-authority refusal cannot be misread as a precondition.
//   - The unknown-flag usage error a pre-#4682 bd emits for --if-revision yields
//     ErrConditionalWriteUnsupported (the interim probe-miss signal). It is
//     ANCHORED to "unknown flag: --if-revision" so a capable bd's usage echo —
//     which merely lists the flag — can never latch the store incapable.
//   - The ambiguous connection class outranks any remaining gate-refusal or
//     field-based precondition guess: the write MAY have committed, so it is
//     surfaced as-is for the caller's self-win contract rather than reported as a
//     definitive did-not-commit.
//   - bd's not-found phrasings map to ErrNotFound so delete/close stay idempotent.
//   - Any other machine code is a per-write *GateRefusalError (never latches).
//   - A code-less body with revision fields or a precondition message is a
//     defensive precondition (bd omitted the code); everything else surfaces as-is.
//
// The precondition ID and Expected are finalized by the calling verb wrapper:
// Expected is always the caller's own snapshot argument, and Raw preserves the
// backend body when the two disagree. bd #4682 is unlanded, so the
// precondition/unsupported substrings are provisional; the //go:build integration
// conformance row (S2-T12) is the authoritative guard.
func classifyConditionalWriteResult(out []byte, err error) error {
	if err == nil {
		return nil
	}
	body, bodyOK := parseBdConditionalErrorBody(out, err)
	msg := err.Error()

	// A recognized machine body code is the authoritative discriminator: it
	// dominates the revision fields AND the message heuristics below. A present
	// body also means bd actually answered (it did not drop mid-write), so a code
	// legitimately outranks the ambiguous-connection class too.
	if bodyOK {
		switch body.Code {
		case bdConditionalCodePreconditionFailed:
			return newPreconditionFailed(body, out, err)
		case bdConditionalCodeUnsupported:
			return ErrConditionalWriteUnsupported
		}
	}

	// Interim unsupported signal: a pre-#4682 bd rejects --if-revision as an
	// unknown flag. Anchored so a capable bd's usage echo cannot latch it.
	if isBdUnknownIfRevisionFlag(msg) {
		return ErrConditionalWriteUnsupported
	}

	// Ambiguous connection class: the write MAY have committed. This outranks the
	// message-based gate/not-found/precondition heuristics below (all of which
	// would wrongly tell the caller definitively what happened), but not a
	// recognized machine code above, which proves bd answered.
	if isBdAmbiguousWriteError(err) {
		return err
	}

	// Any other machine code is a per-write policy gate refusal (never latches).
	// It precedes the message not-found heuristic so a refusal whose human text
	// merely contains "not found" (e.g. "lease not found for holder") is not
	// silently swallowed into idempotent success.
	if bodyOK && body.Code != "" {
		return &GateRefusalError{Code: body.Code, Raw: conditionalRawDetail(out, err)}
	}

	// Code-less not-found stays idempotent for delete/close callers.
	if isBdNotFound(err) {
		return ErrNotFound
	}

	// Code-less defensive precondition: revision fields present, or bd emitted
	// only a human precondition message.
	if isBdConditionalPrecondition(body, msg) {
		return newPreconditionFailed(body, out, err)
	}

	return err
}

// newPreconditionFailed builds a *PreconditionFailedError from a classified body,
// filling Expected/Current when the backend supplied them (zero otherwise) and
// always preserving the raw body for forensics.
func newPreconditionFailed(body bdConditionalErrorBody, out []byte, err error) *PreconditionFailedError {
	pfe := &PreconditionFailedError{Raw: conditionalRawDetail(out, err)}
	if body.ExpectedRevision != nil {
		pfe.Expected = *body.ExpectedRevision
	}
	if body.CurrentRevision != nil {
		pfe.Current = *body.CurrentRevision
	}
	return pfe
}

// isBdConditionalPrecondition reports whether a code-less failure is nonetheless a
// revision-precondition mismatch, inferred from revision fields or a precondition
// message (both hyphenated and spaced forms bd might use).
func isBdConditionalPrecondition(body bdConditionalErrorBody, msg string) bool {
	if body.ExpectedRevision != nil || body.CurrentRevision != nil {
		return true
	}
	lower := strings.ToLower(msg)
	return strings.Contains(lower, "precondition failed") ||
		strings.Contains(lower, "precondition-failed") ||
		strings.Contains(lower, "revision mismatch")
}

// isBdUnknownIfRevisionFlag matches the usage error a bd without revision-CAS
// support emits for --if-revision. It is ANCHORED to the flag name immediately
// following the parser's "unknown flag" / "not defined" marker: a cobra usage
// echo lists --if-revision in its flags block on ANY flag error, so a floating
// "contains if-revision" check would latch a CAPABLE bd the moment gascity passed
// some unrelated unknown flag — the exact silent-degrade the latch must avoid.
func isBdUnknownIfRevisionFlag(msg string) bool {
	lower := strings.ToLower(msg)
	for _, anchor := range []string{
		"unknown flag: --if-revision",
		"unknown flag: -if-revision",
		"unknown flag '--if-revision'",
		"flag provided but not defined: -if-revision",
		"flag provided but not defined: --if-revision",
	} {
		if strings.Contains(lower, anchor) {
			return true
		}
	}
	return false
}

// conditionalRawDetail returns a bounded forensic snapshot of a failed
// conditional write, preferring the command output and falling back to the error
// string.
func conditionalRawDetail(out []byte, err error) string {
	if len(bytes.TrimSpace(out)) > 0 {
		return truncateRawOutput(out, 512)
	}
	if err != nil {
		return err.Error()
	}
	return ""
}
