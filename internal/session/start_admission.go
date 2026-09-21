package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/citylayout"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/fslock"
	"github.com/gastownhall/gascity/internal/runtime"
)

// ErrStartDeferred means admission could not safely grant a new runtime slot.
// It is backpressure, not a failed agent invocation or a crash.
var ErrStartDeferred = errors.New("session start deferred by city capacity")

// StartAdmission enforces a city's launch ceiling across controller, CLI and
// API processes. It observes runtime state, never a persisted running counter.
type StartAdmission struct {
	cityPath        string
	limit           int
	exemptTemplates []string
	store           beads.Store
	provider        runtime.Provider
}

// NewStartAdmission snapshots the configured launch policy. A nil policy is
// unlimited. The provider must observe every runtime backend in this city.
func NewStartAdmission(cityPath string, cfg *config.City, store beads.Store, provider runtime.Provider) *StartAdmission {
	if cfg == nil || cfg.Workspace.MaxActiveSessions == nil || *cfg.Workspace.MaxActiveSessions < 0 {
		return nil
	}
	return &StartAdmission{
		cityPath: cityPath, limit: *cfg.Workspace.MaxActiveSessions,
		exemptTemplates: slices.Clone(cfg.Workspace.SessionLimitExemptTemplates), store: store, provider: provider,
	}
}

// WithStartAdmission applies the same admission policy to all manager launch
// paths, including create, resume, runtime-only start and stale-key retry.
func WithStartAdmission(admission *StartAdmission) ManagerOption {
	return func(m *Manager) { m.startAdmission = admission }
}

func (m *Manager) startRuntime(ctx context.Context, name string, cfg runtime.Config) error {
	return m.startAdmission.Start(ctx, m.sp, name, cfg)
}

// Start serializes census-and-launch across OS processes. The advisory lock
// contains no liveness data and is released by the OS on process death. Holding
// it through Start also counts in-flight launches, without stale reservations.
// No session metadata is written while holding this admission lock.
func (a *StartAdmission) Start(ctx context.Context, sp runtime.Provider, name string, cfg runtime.Config) error {
	if a == nil {
		return sp.Start(ctx, name, cfg)
	}
	if a.cityPath == "" || a.provider == nil {
		return fmt.Errorf("%w: missing city runtime context", ErrStartDeferred)
	}
	lockDir := citylayout.RuntimePath(a.cityPath)
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		return fmt.Errorf("%w: admission directory: %w", ErrStartDeferred, err)
	}
	lock, err := os.OpenFile(filepath.Join(lockDir, "session-start-admission.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("%w: admission lock: %w", ErrStartDeferred, err)
	}
	defer func() { _ = lock.Close() }() // Closing releases the OS lock, including error paths.
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrStartDeferred, err)
		}
		if err := fslock.TryLockEx(lock); err == nil {
			break
		} else if !fslock.WouldBlock(err) {
			return fmt.Errorf("%w: admission lock: %w", ErrStartDeferred, err)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("%w: %w", ErrStartDeferred, ctx.Err())
		case <-timer.C:
		}
	}
	if err := a.check(ctx, name); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrStartDeferred, err)
	}
	return sp.Start(ctx, name, cfg)
}

func (a *StartAdmission) check(ctx context.Context, name string) error {
	running, err := runtime.ListSessionsForAdmission(ctx, a.provider)
	if err != nil {
		return fmt.Errorf("%w: runtime census: %w", ErrStartDeferred, err)
	}
	// Let the provider retain its duplicate-start semantics at capacity.
	if slices.Contains(running, name) {
		return nil
	}
	exemptNames := make(map[string]bool)
	if len(a.exemptTemplates) > 0 {
		if a.store == nil {
			return fmt.Errorf("%w: missing session store for exemptions", ErrStartDeferred)
		}
		infos, err := NewStore(beads.SessionStore{Store: a.store}).ListLabeledSessionInfosUnfiltered()
		if err != nil {
			return fmt.Errorf("%w: session census: %w", ErrStartDeferred, err)
		}
		for _, info := range infos {
			// Conflicting identities never turn a model session into an exemption.
			exempt := slices.Contains(a.exemptTemplates, info.Template)
			if previous, seen := exemptNames[info.SessionName]; seen {
				exempt = exempt && previous
			}
			exemptNames[info.SessionName] = exempt
		}
	}
	if exemptNames[name] {
		return nil
	}
	counted := make(map[string]bool)
	for _, liveName := range running {
		if !exemptNames[liveName] {
			counted[liveName] = true
		}
	}
	if len(counted) >= a.limit {
		return fmt.Errorf("%w: %d/%d non-exempt sessions running", ErrStartDeferred, len(counted), a.limit)
	}
	return nil
}
