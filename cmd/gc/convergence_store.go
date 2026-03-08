package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/convergence"
	"github.com/gastownhall/gascity/internal/events"
)

// convergenceStoreAdapter bridges beads.Store to convergence.Store.
type convergenceStoreAdapter struct {
	store beads.Store
}

var _ convergence.Store = (*convergenceStoreAdapter)(nil)

func newConvergenceStoreAdapter(store beads.Store) *convergenceStoreAdapter {
	return &convergenceStoreAdapter{store: store}
}

func (a *convergenceStoreAdapter) GetBead(id string) (convergence.BeadInfo, error) {
	b, err := a.store.Get(id)
	if err != nil {
		return convergence.BeadInfo{}, err
	}
	return beadToInfo(b), nil
}

func (a *convergenceStoreAdapter) GetMetadata(id string) (map[string]string, error) {
	b, err := a.store.Get(id)
	if err != nil {
		return nil, err
	}
	if b.Metadata == nil {
		return map[string]string{}, nil
	}
	return b.Metadata, nil
}

func (a *convergenceStoreAdapter) SetMetadata(id, key, value string) error {
	return a.store.SetMetadata(id, key, value)
}

func (a *convergenceStoreAdapter) CloseBead(id string) error {
	return a.store.Close(id)
}

func (a *convergenceStoreAdapter) Children(parentID string) ([]convergence.BeadInfo, error) {
	children, err := a.store.Children(parentID)
	if err != nil {
		return nil, err
	}
	result := make([]convergence.BeadInfo, len(children))
	for i, b := range children {
		result[i] = beadToInfo(b)
	}
	return result, nil
}

func (a *convergenceStoreAdapter) PourWisp(parentID, formula, idempotencyKey string, vars map[string]string, evaluatePrompt string) (string, error) {
	// Idempotency: check if a wisp with this key already exists (crash-retry safety).
	if existing, found, err := a.FindByIdempotencyKey(idempotencyKey); err == nil && found {
		return existing, nil
	}

	// Build vars list from map.
	var varList []string
	for k, v := range vars {
		varList = append(varList, k+"="+v)
	}
	if evaluatePrompt != "" {
		varList = append(varList, "evaluate_prompt="+evaluatePrompt)
	}
	wispID, err := a.store.MolCookOn(formula, parentID, "", varList)
	if err != nil {
		return "", err
	}
	// Set the idempotency key on the wisp.
	if setErr := a.store.SetMetadata(wispID, "idempotency_key", idempotencyKey); setErr != nil {
		return wispID, fmt.Errorf("wisp created (%s) but failed to set idempotency key: %w", wispID, setErr)
	}
	return wispID, nil
}

func (a *convergenceStoreAdapter) FindByIdempotencyKey(key string) (string, bool, error) {
	// Extract parent bead ID from key format "converge:<bead-id>:iter:<N>".
	parentID := extractParentIDFromKey(key)
	if parentID == "" {
		// Fall back to scanning all beads.
		return a.findByKeyScan(key)
	}
	children, err := a.store.Children(parentID)
	if err != nil {
		// Parent might not exist or have no children — try full scan.
		return a.findByKeyScan(key)
	}
	for _, b := range children {
		if b.Metadata != nil && b.Metadata["idempotency_key"] == key {
			return b.ID, true, nil
		}
	}
	return "", false, nil
}

func (a *convergenceStoreAdapter) findByKeyScan(key string) (string, bool, error) {
	all, err := a.store.List()
	if err != nil {
		return "", false, err
	}
	for _, b := range all {
		if b.Metadata != nil && b.Metadata["idempotency_key"] == key {
			return b.ID, true, nil
		}
	}
	return "", false, nil
}

func (a *convergenceStoreAdapter) CountActiveConvergenceLoops(targetAgent string) (int, error) {
	all, err := a.store.List()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, b := range all {
		if b.Type != "convergence" || b.Status == "closed" {
			continue
		}
		if b.Metadata == nil {
			continue
		}
		state := b.Metadata[convergence.FieldState]
		target := b.Metadata[convergence.FieldTarget]
		if (state == convergence.StateActive || state == convergence.StateWaitingManual) && target == targetAgent {
			count++
		}
	}
	return count, nil
}

func (a *convergenceStoreAdapter) CreateConvergenceBead(title string) (string, error) {
	b, err := a.store.Create(beads.Bead{
		Title:  title,
		Type:   "convergence",
		Status: "in_progress",
	})
	if err != nil {
		return "", err
	}
	return b.ID, nil
}

// beadToInfo converts a beads.Bead to convergence.BeadInfo.
func beadToInfo(b beads.Bead) convergence.BeadInfo {
	info := convergence.BeadInfo{
		ID:        b.ID,
		Status:    b.Status,
		ParentID:  b.ParentID,
		CreatedAt: b.CreatedAt,
	}
	if b.Metadata != nil {
		info.IdempotencyKey = b.Metadata["idempotency_key"]
		// Parse closed_at from metadata if present.
		if ca, ok := b.Metadata["closed_at"]; ok && ca != "" {
			if t, err := time.Parse(time.RFC3339Nano, ca); err == nil {
				info.ClosedAt = t
			}
		}
	}
	// If status is closed but no closed_at metadata, use CreatedAt as fallback
	// (duration will be zero, which is acceptable for v0).
	if b.Status == "closed" && info.ClosedAt.IsZero() {
		info.ClosedAt = b.CreatedAt
	}
	return info
}

// extractParentIDFromKey extracts the bead ID from an idempotency key
// of the form "converge:<bead-id>:iter:<N>".
func extractParentIDFromKey(key string) string {
	if !strings.HasPrefix(key, "converge:") {
		return ""
	}
	rest := key[len("converge:"):]
	idx := strings.Index(rest, ":iter:")
	if idx < 0 {
		return ""
	}
	return rest[:idx]
}

// convergenceEventEmitter wraps events.Recorder to implement convergence.EventEmitter.
type convergenceEventEmitter struct {
	rec events.Recorder
}

var _ convergence.EventEmitter = (*convergenceEventEmitter)(nil)

func (e *convergenceEventEmitter) Emit(eventType, eventID, beadID string, payload json.RawMessage, _ bool) {
	e.rec.Record(events.Event{
		Type:    eventType,
		Actor:   "convergence",
		Subject: beadID,
		Message: string(payload),
	})
	_ = eventID // used for deduplication by consumers, not the recorder
}
