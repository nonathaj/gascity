package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	beadsexec "github.com/gastownhall/gascity/internal/beads/exec"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/formula"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/mail"
	"github.com/gastownhall/gascity/internal/mail/beadmail"
	"github.com/gastownhall/gascity/internal/session"
)

// controllerState implements api.State and api.StateMutator.
// Protected by an RWMutex for hot-reload: readers take RLock,
// the controller loop takes Lock when updating cfg/sp/stores.
type controllerState struct {
	mu         sync.RWMutex
	cfg        *config.City
	sp         session.Provider
	beadStores map[string]beads.Store
	mailProvs  map[string]mail.Provider
	eventProv  events.Provider
	cityName   string
	cityPath   string
}

// newControllerState creates a controllerState with per-rig stores.
func newControllerState(
	cfg *config.City,
	sp session.Provider,
	ep events.Provider,
	cityName, cityPath string,
) *controllerState {
	cs := &controllerState{
		cfg:       cfg,
		sp:        sp,
		eventProv: ep,
		cityName:  cityName,
		cityPath:  cityPath,
	}
	cs.beadStores, cs.mailProvs = cs.buildStores(cfg)
	return cs
}

// buildStores creates bead stores and mail providers for each rig in cfg.
// Pure function of cfg — does not read or write cs fields (safe to call unlocked).
func (cs *controllerState) buildStores(cfg *config.City) (map[string]beads.Store, map[string]mail.Provider) {
	provider := beadsProviderFor(cfg)
	stores := make(map[string]beads.Store, len(cfg.Rigs))
	provs := make(map[string]mail.Provider, len(cfg.Rigs))

	// For the "file" provider, all rigs share the same city-level beads.json
	// and a single mail provider to ensure identity-based dedup works correctly.
	var sharedFileStore beads.Store
	var sharedMailProv mail.Provider
	if provider == "file" {
		store, err := beads.OpenFileStore(fsys.OSFS{}, filepath.Join(cs.cityPath, ".gc", "beads.json"))
		if err == nil {
			sharedFileStore = store
			sharedMailProv = beadmail.New(store)
		} else {
			// Fall back to bd provider rather than opening duplicate per-rig file stores.
			fmt.Fprintf(os.Stderr, "api: failed to open shared file store: %v (falling back to bd provider)\n", err)
			provider = "bd"
		}
	}

	for _, rig := range cfg.Rigs {
		if sharedFileStore != nil {
			stores[rig.Name] = sharedFileStore
			provs[rig.Name] = sharedMailProv
		} else {
			store := cs.openRigStore(provider, rig.Path)
			stores[rig.Name] = store
			provs[rig.Name] = beadmail.New(store)
		}
	}
	return stores, provs
}

// beadsProviderFor returns the bead store provider name from the given config.
// Pure function — does not read controllerState fields.
func beadsProviderFor(cfg *config.City) string {
	if v := os.Getenv("GC_BEADS"); v != "" {
		return v
	}
	if cfg.Beads.Provider != "" {
		return cfg.Beads.Provider
	}
	return "bd"
}

// openRigStore creates a bead store for a rig path using the given provider.
func (cs *controllerState) openRigStore(provider, rigPath string) beads.Store {
	if strings.HasPrefix(provider, "exec:") {
		s := beadsexec.NewStore(strings.TrimPrefix(provider, "exec:"))
		s.SetFormulaResolver(formula.DirResolver(filepath.Join(cs.cityPath, ".gc", "formulas")))
		return s
	}
	switch provider {
	case "file":
		store, err := beads.OpenFileStore(fsys.OSFS{}, filepath.Join(cs.cityPath, ".gc", "beads.json"))
		if err != nil {
			return beads.NewBdStore(rigPath, beads.ExecCommandRunner())
		}
		return store
	default: // "bd" or unrecognized
		return beads.NewBdStore(rigPath, beads.ExecCommandRunner())
	}
}

// update replaces the config, session provider, and reopens stores.
// Stores are built outside the lock to avoid blocking readers during I/O.
func (cs *controllerState) update(cfg *config.City, sp session.Provider) {
	// Build new stores outside the lock (may do file I/O / subprocess spawns).
	stores, provs := cs.buildStores(cfg)

	// Swap under short critical section.
	cs.mu.Lock()
	cs.cfg = cfg
	cs.sp = sp
	cs.beadStores = stores
	cs.mailProvs = provs
	cs.mu.Unlock()
}

// --- api.State implementation ---

// Config returns the current city config snapshot.
func (cs *controllerState) Config() *config.City {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.cfg
}

// SessionProvider returns the current session provider.
func (cs *controllerState) SessionProvider() session.Provider {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.sp
}

// BeadStore returns the bead store for a rig (by name).
func (cs *controllerState) BeadStore(rig string) beads.Store {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.beadStores[rig]
}

// BeadStores returns all rig names and their stores.
func (cs *controllerState) BeadStores() map[string]beads.Store {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	// Return a copy to avoid races.
	m := make(map[string]beads.Store, len(cs.beadStores))
	for k, v := range cs.beadStores {
		m[k] = v
	}
	return m
}

// MailProvider returns the mail provider for a rig.
func (cs *controllerState) MailProvider(rig string) mail.Provider {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.mailProvs[rig]
}

// MailProviders returns all rig names and their mail providers.
func (cs *controllerState) MailProviders() map[string]mail.Provider {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	m := make(map[string]mail.Provider, len(cs.mailProvs))
	for k, v := range cs.mailProvs {
		m[k] = v
	}
	return m
}

// EventProvider returns the event provider.
func (cs *controllerState) EventProvider() events.Provider {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.eventProv
}

// CityName returns the city name.
func (cs *controllerState) CityName() string {
	return cs.cityName
}

// CityPath returns the city root directory.
func (cs *controllerState) CityPath() string {
	return cs.cityPath
}

// --- api.StateMutator implementation ---

// spAndSession captures the session provider and computes the session name
// in a single critical section to avoid TOCTOU with config reloads.
func (cs *controllerState) spAndSession(name string) (session.Provider, string) {
	cs.mu.RLock()
	sp := cs.sp
	tmpl := cs.cfg.Workspace.SessionTemplate
	cs.mu.RUnlock()
	return sp, agent.SessionNameFor(cs.cityName, name, tmpl)
}

// SuspendAgent marks an agent as suspended via session metadata.
func (cs *controllerState) SuspendAgent(name string) error {
	sp, sessionName := cs.spAndSession(name)
	return sp.SetMeta(sessionName, "suspended", "true")
}

// ResumeAgent removes the suspended flag.
func (cs *controllerState) ResumeAgent(name string) error {
	sp, sessionName := cs.spAndSession(name)
	return sp.RemoveMeta(sessionName, "suspended")
}

// KillAgent force-kills the agent session.
func (cs *controllerState) KillAgent(name string) error {
	sp, sessionName := cs.spAndSession(name)
	return sp.Stop(sessionName)
}

// DrainAgent signals graceful wind-down.
func (cs *controllerState) DrainAgent(name string) error {
	sp, sessionName := cs.spAndSession(name)
	return sp.SetMeta(sessionName, "drain", "true")
}

// UndrainAgent cancels a drain signal.
func (cs *controllerState) UndrainAgent(name string) error {
	sp, sessionName := cs.spAndSession(name)
	return sp.RemoveMeta(sessionName, "drain")
}

// NudgeAgent sends a message to a running agent session.
func (cs *controllerState) NudgeAgent(name, message string) error {
	sp, sessionName := cs.spAndSession(name)
	if !sp.IsRunning(sessionName) {
		return fmt.Errorf("agent %q not running", name)
	}
	return sp.Nudge(sessionName, message)
}

// SuspendRig suspends all agents (including expanded pool members) in a rig.
func (cs *controllerState) SuspendRig(name string) error {
	cs.mu.RLock()
	sp := cs.sp
	cfg := cs.cfg
	tmpl := cs.cfg.Workspace.SessionTemplate
	cs.mu.RUnlock()
	if !cs.rigExists(cfg, name) {
		return fmt.Errorf("rig %q not found", name)
	}
	var errs []error
	for _, qn := range cs.expandedAgentNames(cfg, name) {
		sessionName := agent.SessionNameFor(cs.cityName, qn, tmpl)
		if err := sp.SetMeta(sessionName, "suspended", "true"); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ResumeRig resumes all agents (including expanded pool members) in a rig.
func (cs *controllerState) ResumeRig(name string) error {
	cs.mu.RLock()
	sp := cs.sp
	cfg := cs.cfg
	tmpl := cs.cfg.Workspace.SessionTemplate
	cs.mu.RUnlock()
	if !cs.rigExists(cfg, name) {
		return fmt.Errorf("rig %q not found", name)
	}
	var errs []error
	for _, qn := range cs.expandedAgentNames(cfg, name) {
		sessionName := agent.SessionNameFor(cs.cityName, qn, tmpl)
		if err := sp.RemoveMeta(sessionName, "suspended"); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// expandedAgentNames returns qualified names for all agents in a rig,
// expanding pool agents into individual member names (pool-1, pool-2, etc).
func (cs *controllerState) expandedAgentNames(cfg *config.City, rig string) []string {
	var names []string
	for _, a := range cfg.Agents {
		if a.Dir != rig {
			continue
		}
		if !a.IsPool() {
			names = append(names, a.QualifiedName())
			continue
		}
		pool := a.EffectivePool()
		poolMax := pool.Max
		if poolMax <= 0 {
			poolMax = 1
		}
		for i := 1; i <= poolMax; i++ {
			memberName := a.Name
			if poolMax > 1 {
				memberName = fmt.Sprintf("%s-%d", a.Name, i)
			}
			qn := memberName
			if a.Dir != "" {
				qn = a.Dir + "/" + memberName
			}
			names = append(names, qn)
		}
	}
	return names
}

// rigExists checks if a rig name exists in the config.
func (cs *controllerState) rigExists(cfg *config.City, name string) bool {
	for _, r := range cfg.Rigs {
		if r.Name == name {
			return true
		}
	}
	return false
}
