// Package auto provides a composite [session.Provider] that routes
// sessions to a default backend (typically tmux) or ACP based on
// per-session registration. Sessions are registered as ACP via
// [Provider.RouteACP] before [Provider.Start] is called. Unregistered
// sessions route to the default backend.
package auto

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gastownhall/gascity/internal/session"
)

// Provider routes session operations to a default or ACP backend
// based on per-session registration.
type Provider struct {
	defaultSP session.Provider
	acpSP     session.Provider

	mu     sync.RWMutex
	routes map[string]bool // true = ACP
}

var _ session.Provider = (*Provider)(nil)

// New creates a composite provider. defaultSP handles sessions not
// registered as ACP. acpSP handles sessions registered via RouteACP.
func New(defaultSP, acpSP session.Provider) *Provider {
	return &Provider{
		defaultSP: defaultSP,
		acpSP:     acpSP,
		routes:    make(map[string]bool),
	}
}

// RouteACP registers a session name to use the ACP backend.
// Must be called before Start for that session.
func (p *Provider) RouteACP(name string) {
	p.mu.Lock()
	p.routes[name] = true
	p.mu.Unlock()
}

// Unroute removes a session's routing entry. Called on Stop to avoid
// leaking entries for destroyed sessions.
func (p *Provider) Unroute(name string) {
	p.mu.Lock()
	delete(p.routes, name)
	p.mu.Unlock()
}

func (p *Provider) route(name string) session.Provider {
	p.mu.RLock()
	isACP := p.routes[name]
	p.mu.RUnlock()
	if isACP {
		return p.acpSP
	}
	return p.defaultSP
}

// Start delegates to the routed backend.
func (p *Provider) Start(ctx context.Context, name string, cfg session.Config) error {
	return p.route(name).Start(ctx, name, cfg)
}

// Stop delegates to the routed backend and cleans up the route entry.
func (p *Provider) Stop(name string) error {
	err := p.route(name).Stop(name)
	p.Unroute(name)
	return err
}

// Interrupt delegates to the routed backend.
func (p *Provider) Interrupt(name string) error {
	return p.route(name).Interrupt(name)
}

// IsRunning checks both backends. Returns true if either reports running.
func (p *Provider) IsRunning(name string) bool {
	return p.route(name).IsRunning(name)
}

// IsAttached delegates to the routed backend.
func (p *Provider) IsAttached(name string) bool {
	return p.route(name).IsAttached(name)
}

// Attach delegates to the routed backend. ACP sessions return an error.
func (p *Provider) Attach(name string) error {
	p.mu.RLock()
	isACP := p.routes[name]
	p.mu.RUnlock()
	if isACP {
		return fmt.Errorf("agent %q uses ACP transport (no terminal to attach to)", name)
	}
	return p.defaultSP.Attach(name)
}

// ProcessAlive delegates to the routed backend.
func (p *Provider) ProcessAlive(name string, processNames []string) bool {
	return p.route(name).ProcessAlive(name, processNames)
}

// Nudge delegates to the routed backend.
func (p *Provider) Nudge(name, message string) error {
	return p.route(name).Nudge(name, message)
}

// SetMeta delegates to the routed backend.
func (p *Provider) SetMeta(name, key, value string) error {
	return p.route(name).SetMeta(name, key, value)
}

// GetMeta delegates to the routed backend.
func (p *Provider) GetMeta(name, key string) (string, error) {
	return p.route(name).GetMeta(name, key)
}

// RemoveMeta delegates to the routed backend.
func (p *Provider) RemoveMeta(name, key string) error {
	return p.route(name).RemoveMeta(name, key)
}

// Peek delegates to the routed backend.
func (p *Provider) Peek(name string, lines int) (string, error) {
	return p.route(name).Peek(name, lines)
}

// ListRunning queries both backends and merges results.
func (p *Provider) ListRunning(prefix string) ([]string, error) {
	defaultList, dErr := p.defaultSP.ListRunning(prefix)
	acpList, aErr := p.acpSP.ListRunning(prefix)
	if dErr != nil && aErr != nil {
		return nil, dErr
	}
	return append(defaultList, acpList...), nil
}

// GetLastActivity delegates to the routed backend.
func (p *Provider) GetLastActivity(name string) (time.Time, error) {
	return p.route(name).GetLastActivity(name)
}

// ClearScrollback delegates to the routed backend.
func (p *Provider) ClearScrollback(name string) error {
	return p.route(name).ClearScrollback(name)
}

// CopyTo delegates to the routed backend.
func (p *Provider) CopyTo(name, src, relDst string) error {
	return p.route(name).CopyTo(name, src, relDst)
}

// SendKeys delegates to the routed backend.
func (p *Provider) SendKeys(name string, keys ...string) error {
	return p.route(name).SendKeys(name, keys...)
}

// RunLive delegates to the routed backend.
func (p *Provider) RunLive(name string, cfg session.Config) error {
	return p.route(name).RunLive(name, cfg)
}
