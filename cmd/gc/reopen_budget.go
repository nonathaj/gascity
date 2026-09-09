package main

import (
	"sync"
	"time"
)

// Reopen budget for the dead-assignee repair path.
//
// Reopening a bead whose assignee died is correct repair, and the common case
// is one reopen that sticks. The failure REQ-005 names is the case that does
// not stick: the same bead reopened over and over — 73 times across 5 hours —
// while nothing raised an alarm, because each individual reopen looked like
// routine maintenance. An unbounded repair loop that never escalates is
// indistinguishable from a healthy system right up until someone reads the
// logs.
//
// So the sweep spends a budget instead of repairing forever. Each bead may be
// reopened reopenBudgetLimit times inside one rolling patrol window; the reopen
// that spends the budget raises the alarm-level bead.reopen_budget_exhausted
// event, and further reopens of that bead are refused until its window elapses.
// Refusing is the safe side: the bead stays exactly as the dead worker left it,
// visible and untouched, for an operator or a later window to resolve.
// Reopening it a 74th time is what loses the signal.
//
// The window is rolling and per bead, anchored at the bead's first reopen, so a
// bead that flapped hours ago is not punished for it and a bead that has
// genuinely settled starts clean.
//
// No status files (AGENTS.md): the counter is process-local cache state, queried
// live and rebuilt from observation. A controller restart re-arms every budget,
// which is the right bias — a restart is precisely when the operator wants
// repair attempted again, and a bead that is still wedged re-earns its alarm
// within one window.
//
// The budget is owned by its caller rather than kept in a package global: the
// controller holds one for the life of the process (that is what makes the
// window span patrol ticks), one-shot paths hold one for the run, and each test
// gets its own. Shared mutable module state would make these budgets leak into
// each other, and bead IDs are not unique across stores (see the cross-store
// ID-collision case) so a process-wide map keyed on ID alone would also let two
// genuinely different beads spend one another's budget.

const (
	// reopenBudgetLimit is how many times the dead-assignee path may reopen the
	// same bead inside one patrol window. Three, matching
	// detachedProbeErrorThreshold: two reopens is within noise for a genuinely
	// flapping session, three is a pattern. At the default 30s patrol interval
	// this alarms in minutes, not the five hours REQ-005 rejects.
	reopenBudgetLimit = 3

	// reopenBudgetWindow is the rolling per-bead window the reopens are counted
	// over. Wide enough that a real flap accumulates across many patrol ticks
	// (30s default → dozens of ticks), narrow enough that unrelated reopens
	// spread across an afternoon never sum into a false alarm.
	reopenBudgetWindow = 15 * time.Minute

	// reopenBudgetSweepThreshold is the entry count past which note reclaims the
	// entries whose windows have closed.
	//
	// The controller's budget lives as long as the process, so an entry that is
	// never reclaimed is held for the controller's uptime. A closed window can
	// only ever be overwritten, never consulted, so those entries are dead
	// weight — without this the map would grow with every distinct bead the
	// dead-assignee sweep has EVER reopened.
	//
	// 256 sits far above any healthy steady state (a bead only lands here by
	// having its assignee die) while keeping the map small. Reclamation is an
	// O(n) scan under the lock, but note runs only on a reopen that actually
	// landed — a handful of times per patrol tick at worst — so scanning a few
	// hundred entries there is not a cost worth amortizing away with a dynamic
	// threshold. Sweeping eagerly keeps the map at the live-entry count instead
	// of at its historical peak.
	reopenBudgetSweepThreshold = 256
)

// reopenBudgetEntry is one bead's spend inside its current window.
type reopenBudgetEntry struct {
	count       int
	windowStart time.Time
}

// reopenBudget tracks per-bead reopen spend for the dead-assignee path over one
// owner's lifetime. Safe for concurrent use: the controller sweeps from its
// patrol tick while other goroutines may hold the same budget.
type reopenBudget struct {
	mu     sync.Mutex
	byBead map[string]reopenBudgetEntry
}

// newReopenBudget returns an empty budget, every bead fully funded.
func newReopenBudget() *reopenBudget {
	return &reopenBudget{byBead: make(map[string]reopenBudgetEntry)}
}

// reopenBudgetKey identifies a bead for budgeting. Bead IDs are unique only
// within a store, so the owning store ref is part of the key: two rigs can each
// hold a bead called gc-1, and they must not share a budget.
func reopenBudgetKey(storeRef, beadID string) string {
	return storeRef + "\x00" + beadID
}

// allows reports whether the dead-assignee path may reopen key now. It is a
// read-only predicate — call note only after the reopen actually lands, so a
// refused or failed release never spends budget.
//
// An entry whose window has elapsed is treated as spent-nothing; the entry is
// not rewritten here, because note re-anchors the window on the write path
// where the decision is committed. A nil budget allows everything.
func (b *reopenBudget) allows(key string, now time.Time) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.byBead[key]
	if !ok || reopenBudgetWindowElapsed(entry, now) {
		return true
	}
	return entry.count < reopenBudgetLimit
}

// note records one completed reopen of key and returns the reopen count inside
// that bead's current window (1 for the first reopen of a fresh window). A count
// that reaches reopenBudgetLimit means this reopen spent the budget and must
// raise the alarm. A nil budget counts nothing and returns 0.
func (b *reopenBudget) note(key string, now time.Time) int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.byBead == nil {
		b.byBead = make(map[string]reopenBudgetEntry)
	}
	entry, ok := b.byBead[key]
	if !ok || reopenBudgetWindowElapsed(entry, now) {
		entry = reopenBudgetEntry{windowStart: now}
	}
	entry.count++
	b.byBead[key] = entry
	if len(b.byBead) > reopenBudgetSweepThreshold {
		b.reclaimElapsedLocked(now)
	}
	return entry.count
}

// reclaimElapsedLocked drops every entry whose window has closed. Such an entry
// is indistinguishable from an absent one — allows and note both treat an
// elapsed window as a fresh, fully funded bead — so removing it cannot change
// any decision, only the memory held to make it.
//
// Callers must hold b.mu.
func (b *reopenBudget) reclaimElapsedLocked(now time.Time) {
	for key, entry := range b.byBead {
		if reopenBudgetWindowElapsed(entry, now) {
			delete(b.byBead, key)
		}
	}
}

// reopenBudgetWindowElapsed reports whether entry's window has closed by now.
// A zero windowStart (an entry that predates a clock read) counts as elapsed so
// a malformed entry fails toward allowing repair rather than wedging the bead.
func reopenBudgetWindowElapsed(entry reopenBudgetEntry, now time.Time) bool {
	if entry.windowStart.IsZero() {
		return true
	}
	return !now.Before(entry.windowStart.Add(reopenBudgetWindow))
}
