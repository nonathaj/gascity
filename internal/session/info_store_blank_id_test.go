// Fork-local pins: an unresolved nudge target never dead-letters a queued
// nudge. Offered upstream.

package session

import (
	"errors"
	"testing"
)

// TestStoreGetRejectsBlankIDSessionBead pins that Get and GetPersistedResponse
// refuse a stored session bead whose ID is blank or whitespace with
// ErrSessionNotFound. Nudge-target resolution depends on this: a target
// resolved through Get always carries a non-empty session ID, so it can never
// be the unresolved claimant that dead-letters a queued nudge.
func TestStoreGetRejectsBlankIDSessionBead(t *testing.T) {
	for _, id := range []string{"", "   "} {
		store := seedSessionStore(t, sessionBeadFixture(id, "open", map[string]string{
			"session_name":       "sess-worker",
			"continuation_epoch": "7",
		}))
		is := NewStore(store)
		if _, err := is.Get(id); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("Get(%q) err = %v, want ErrSessionNotFound", id, err)
		}
		if _, _, err := is.GetPersistedResponse(id); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("GetPersistedResponse(%q) err = %v, want ErrSessionNotFound", id, err)
		}
	}
}
