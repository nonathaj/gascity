package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/sessionlog"
)

var (
	// ErrNoPendingInteraction reports that a session has nothing awaiting
	// user input or approval resolution.
	ErrNoPendingInteraction = errors.New("session has no pending interaction")
	// ErrInteractionUnsupported reports that the backing runtime cannot
	// surface or resolve structured pending interactions.
	ErrInteractionUnsupported = errors.New("session provider does not support interactive responses")
)

func (m *Manager) sessionBead(id string) (beads.Bead, string, error) {
	b, err := m.store.Get(id)
	if err != nil {
		return beads.Bead{}, "", fmt.Errorf("getting session: %w", err)
	}
	if b.Type != BeadType {
		return beads.Bead{}, "", fmt.Errorf("bead %s is not a session (type=%q)", id, b.Type)
	}
	if b.Status == "closed" {
		return beads.Bead{}, "", fmt.Errorf("session %s is closed", id)
	}

	sessName := b.Metadata["session_name"]
	if sessName == "" {
		sessName = sessionNameFor(id)
	}
	return b, sessName, nil
}

func (m *Manager) ensureRunning(ctx context.Context, id string, b beads.Bead, sessName, resumeCommand string, hints runtime.Config) error {
	if State(b.Metadata["state"]) != StateSuspended && m.sp.IsRunning(sessName) {
		return nil
	}
	if resumeCommand == "" {
		return fmt.Errorf("session %s is suspended and no resume command provided", id)
	}

	cfg := hints
	cfg.Command = resumeCommand
	if cfg.WorkDir == "" {
		cfg.WorkDir = b.Metadata["work_dir"]
	}
	if err := m.sp.Start(ctx, sessName, cfg); err != nil {
		return fmt.Errorf("resuming session: %w", err)
	}
	if err := m.store.SetMetadata(id, "state", string(StateActive)); err != nil {
		_ = m.sp.Stop(sessName)
		return fmt.Errorf("updating session state: %w", err)
	}
	return nil
}

// Send resumes a suspended session if needed, then nudges the runtime with a
// new user message.
func (m *Manager) Send(ctx context.Context, id, message, resumeCommand string, hints runtime.Config) error {
	b, sessName, err := m.sessionBead(id)
	if err != nil {
		return err
	}
	if err := m.ensureRunning(ctx, id, b, sessName, resumeCommand, hints); err != nil {
		return err
	}
	if err := m.sp.Nudge(sessName, message); err != nil {
		return fmt.Errorf("sending message to session: %w", err)
	}
	return nil
}

// StopTurn issues a soft interrupt for the currently running turn.
func (m *Manager) StopTurn(id string) error {
	b, sessName, err := m.sessionBead(id)
	if err != nil {
		return err
	}
	if State(b.Metadata["state"]) == StateSuspended || !m.sp.IsRunning(sessName) {
		return nil
	}
	if err := m.sp.Interrupt(sessName); err != nil {
		return fmt.Errorf("interrupting session: %w", err)
	}
	return nil
}

// Pending returns the provider's current structured pending interaction, if
// the provider supports that capability.
func (m *Manager) Pending(id string) (*runtime.PendingInteraction, bool, error) {
	_, sessName, err := m.sessionBead(id)
	if err != nil {
		return nil, false, err
	}
	ip, ok := m.sp.(runtime.InteractionProvider)
	if !ok {
		return nil, false, nil
	}
	pending, err := ip.Pending(sessName)
	if err != nil {
		return nil, true, fmt.Errorf("getting pending interaction: %w", err)
	}
	return pending, true, nil
}

// Respond resolves the current pending interaction for a session.
func (m *Manager) Respond(id string, response runtime.InteractionResponse) error {
	_, sessName, err := m.sessionBead(id)
	if err != nil {
		return err
	}
	ip, ok := m.sp.(runtime.InteractionProvider)
	if !ok {
		return ErrInteractionUnsupported
	}
	pending, err := ip.Pending(sessName)
	if err != nil {
		return fmt.Errorf("getting pending interaction: %w", err)
	}
	if pending == nil {
		return ErrNoPendingInteraction
	}
	if response.RequestID == "" {
		response.RequestID = pending.RequestID
	}
	if response.Action == "" {
		return fmt.Errorf("interaction action is required")
	}
	if pending.RequestID != "" && response.RequestID != pending.RequestID {
		return fmt.Errorf("pending interaction %q does not match request %q", pending.RequestID, response.RequestID)
	}
	if err := ip.Respond(sessName, response); err != nil {
		return fmt.Errorf("responding to pending interaction: %w", err)
	}
	return nil
}

// TranscriptPath resolves the best available session transcript file.
// It prefers session-key-specific lookup and falls back to workdir-based
// discovery for providers that do not expose a stable session key.
func (m *Manager) TranscriptPath(id string, searchPaths []string) (string, error) {
	b, _, err := m.sessionBead(id)
	if err != nil {
		return "", err
	}
	workDir := b.Metadata["work_dir"]
	if workDir == "" {
		return "", nil
	}
	if len(searchPaths) == 0 {
		searchPaths = sessionlog.DefaultSearchPaths()
	}
	if sessionKey := b.Metadata["session_key"]; sessionKey != "" {
		if path := sessionlog.FindSessionFileByID(searchPaths, workDir, sessionKey); path != "" {
			return path, nil
		}
	}
	return sessionlog.FindSessionFile(searchPaths, workDir), nil
}
