package tmux

import (
	"context"
	"errors"
	"fmt"

	"github.com/gastownhall/gascity/internal/runtime"
)

// ListSessionsForAdmission distinguishes a safely absent named server from a
// census failure without weakening ListRunning's reconciler outage semantics.
func (p *Provider) ListSessionsForAdmission(ctx context.Context) ([]string, error) {
	names, err := p.ListRunning("")
	if err == nil || !errors.Is(err, ErrNoServer) || p.tm.cfg.SocketName == "" {
		return names, err
	}
	// An empty live server can report no current target. Unlike a generic
	// no-server response, this proves the server answered with zero sessions.
	if errors.Is(err, ErrNoCurrentTarget) {
		return nil, nil
	}
	observer := p.tm.serverSocketObserver
	if observer == nil {
		observer = observeNamedSocket
	}
	if socketErr := observer(ctx, namedSocketPath(p.tm.cfg.SocketName)); socketErr != nil {
		return nil, fmt.Errorf("%w: admission census: %w", runtime.ErrRuntimeUnavailable, socketErr)
	}
	return nil, nil
}
