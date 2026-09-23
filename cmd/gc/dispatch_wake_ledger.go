package main

import (
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
)

// A control dispatcher's --follow loop wakes on every bead.* event the city
// emits, and each wake is a readiness scan. Almost all of those events are other
// stores' — measured on a live city over 3.4 hours, 97% of 3,720 bead events
// were minted outside the gascity rig's ledger, 78% in the city store alone,
// most of them session-bead heartbeats — so every rig dispatcher re-scanned its
// own ledger once per foreign event, and each wake also reset the idle backoff
// to its 1s floor (gcty-fz6r).
//
// The wake is narrowed to the ledger the scan reads. That can only ever delay a
// scan, never lose one: the loop's idle sweep re-scans on its timer whatever the
// event stream says, so a change these events do not attribute to the ledger — a
// cross-store blocker closing, a bead minted under a legacy prefix — is still
// picked up within one sweep (workflowServeMaxIdleSleep).

// ledgerWakeWatcher drops bead events whose subject another ledger minted.
// Everything else passes through untouched: non-bead events and errors are the
// wait loop's to judge, and a bead event with no subject cannot be attributed,
// so it wakes as before.
type ledgerWakeWatcher struct {
	events.Watcher
	prefix string
}

// newLedgerWakeWatcher narrows inner to bead events minted under prefix. An
// empty prefix means the scan's ledgers are not known to share one, and inner is
// returned as is.
func newLedgerWakeWatcher(inner events.Watcher, prefix string) events.Watcher {
	if prefix == "" {
		return inner
	}
	return ledgerWakeWatcher{Watcher: inner, prefix: prefix}
}

// Next returns the next event that is not another ledger's bead event.
func (w ledgerWakeWatcher) Next() (events.Event, error) {
	for {
		evt, err := w.Watcher.Next()
		if err != nil || !workflowEventRelevant(evt) || evt.Subject == "" || beads.IDHasPrefix(evt.Subject, w.prefix) {
			return evt, err
		}
		workflowTracef("serve ignore-foreign-event type=%s subject=%s", evt.Type, evt.Subject)
	}
}

// workflowServeWakeLedgerPrefix returns the issue prefix of the one ledger a
// control dispatcher's readiness scan reads, or "" when the wake must not be
// narrowed:
//
//   - a work_query other than the generated control-ready scan may read any
//     ledger, so nothing about its inputs is known;
//   - on a split city the scan reads the graph binding (instead of, or as well
//     as, the scope store), and the binding holds ids minted under more than
//     the scope's prefix: the copies a migration carried over keep the work
//     ledger's ids;
//   - a scope with no resolvable prefix names no ledger.
func workflowServeWakeLedgerPrefix(cityPath, storePath, workQuery string, cfg *config.City) string {
	if _, ok := parseControlReadyQuery(workQuery); !ok {
		return ""
	}
	if _, relocated := controlGraphBinding(cityPath, storePath); relocated {
		return ""
	}
	if _, federated := controlGraphExtraLeg(cityPath, storePath); federated {
		return ""
	}
	return issuePrefixForScope(resolveStoreScopeRoot(cityPath, storePath), cityPath, cfg)
}
