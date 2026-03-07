package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/api"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/fsys"
	"github.com/gastownhall/gascity/internal/mail"
	"github.com/gastownhall/gascity/internal/session"
	"github.com/gastownhall/gascity/internal/supervisor"
	"github.com/spf13/cobra"
)

func newSupervisorCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "supervisor",
		Short: "Manage the machine-wide supervisor",
		Long: `Manage the machine-wide supervisor daemon.

The supervisor manages all registered cities from a single process,
hosting a unified API server. Use "gc register" to add cities.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newSupervisorStartCmd(stdout, stderr),
		newSupervisorStopCmd(stdout, stderr),
		newSupervisorStatusCmd(stdout, stderr),
	)
	return cmd
}

func newSupervisorStartCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the machine-wide supervisor (foreground)",
		Long: `Start the machine-wide supervisor in the foreground.

The supervisor reads ~/.gc/cities.toml for registered cities and
~/.gc/supervisor.toml for configuration. It starts a CityRuntime
for each registered city and hosts a single API server.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if runSupervisor(stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

func newSupervisorStopCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the machine-wide supervisor",
		Long:  `Stop the running machine-wide supervisor and all its cities.`,
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if stopSupervisor(stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

func newSupervisorStatusCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check if the supervisor is running",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if supervisorStatus(stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	return cmd
}

// acquireSupervisorLock takes an exclusive flock on the supervisor lock file.
func acquireSupervisorLock() (*os.File, error) {
	dir := supervisor.RuntimeDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating runtime dir: %w", err)
	}
	path := filepath.Join(dir, "supervisor.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening supervisor lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close() //nolint:errcheck
		return nil, fmt.Errorf("supervisor already running")
	}
	return f, nil
}

// supervisorSocketPath returns the path to the supervisor control socket.
func supervisorSocketPath() string {
	return filepath.Join(supervisor.RuntimeDir(), "supervisor.sock")
}

// supervisorAlive checks whether the supervisor is running by pinging
// the control socket. Returns the PID if alive, 0 otherwise.
func supervisorAlive() int {
	sockPath := supervisorSocketPath()
	conn, err := net.DialTimeout("unix", sockPath, 500*time.Millisecond)
	if err != nil {
		return 0
	}
	defer conn.Close()                                    //nolint:errcheck
	conn.Write([]byte("ping\n"))                          //nolint:errcheck
	conn.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil || n == 0 {
		return 0
	}
	pid, err := strconv.Atoi(string(buf[:n]))
	if err != nil {
		return 0
	}
	return pid
}

// stopSupervisor sends a stop command to the running supervisor.
func stopSupervisor(stdout, stderr io.Writer) int {
	sockPath := supervisorSocketPath()
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		fmt.Fprintln(stderr, "gc supervisor stop: supervisor is not running") //nolint:errcheck
		return 1
	}
	defer conn.Close()                                     //nolint:errcheck
	conn.Write([]byte("stop\n"))                           //nolint:errcheck
	conn.SetReadDeadline(time.Now().Add(10 * time.Second)) //nolint:errcheck
	buf := make([]byte, 64)
	n, _ := conn.Read(buf)
	if n > 0 && string(buf[:n]) == "ok\n" {
		fmt.Fprintln(stdout, "Supervisor stopping...") //nolint:errcheck
		return 0
	}
	fmt.Fprintln(stderr, "gc supervisor stop: no acknowledgment from supervisor") //nolint:errcheck
	return 1
}

// supervisorStatus checks and reports whether the supervisor is running.
func supervisorStatus(stdout, _ io.Writer) int {
	pid := supervisorAlive()
	if pid > 0 {
		fmt.Fprintf(stdout, "Supervisor is running (PID %d)\n", pid) //nolint:errcheck
		return 0
	}
	fmt.Fprintln(stdout, "Supervisor is not running") //nolint:errcheck
	return 1
}

// managedCity tracks a running CityRuntime inside the supervisor.
type managedCity struct {
	cr     *CityRuntime
	cancel context.CancelFunc
	done   chan struct{} // closed when the city goroutine exits
}

// runSupervisor is the main supervisor loop. It acquires the lock,
// starts a control socket, reads the registry, starts CityRuntimes,
// and runs until canceled.
func runSupervisor(stdout, stderr io.Writer) int {
	lock, err := acquireSupervisorLock()
	if err != nil {
		fmt.Fprintf(stderr, "gc supervisor: %v\n", err) //nolint:errcheck
		return 1
	}
	defer lock.Close() //nolint:errcheck

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handler.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	// Control socket.
	sockPath := supervisorSocketPath()
	if err := os.MkdirAll(filepath.Dir(sockPath), 0o700); err != nil {
		fmt.Fprintf(stderr, "gc supervisor: creating socket dir: %v\n", err) //nolint:errcheck
		return 1
	}
	lis, err := startControllerSocket(filepath.Dir(sockPath), cancel)
	if err != nil {
		fmt.Fprintf(stderr, "gc supervisor: %v\n", err) //nolint:errcheck
		return 1
	}
	defer lis.Close()         //nolint:errcheck
	defer os.Remove(sockPath) //nolint:errcheck

	// Load supervisor config.
	supCfg, err := supervisor.LoadConfig(supervisor.ConfigPath())
	if err != nil {
		fmt.Fprintf(stderr, "gc supervisor: config: %v\n", err) //nolint:errcheck
		return 1
	}

	reg := supervisor.NewRegistry(supervisor.RegistryPath())

	// Track managed cities.
	var mu sync.Mutex
	cities := make(map[string]*managedCity)

	// Start API server with a multi-city state dispatcher.
	mcs := &multiCityState{cities: cities, mu: &mu, startedAt: time.Now()}
	bind := supCfg.Supervisor.BindOrDefault()
	port := supCfg.Supervisor.PortOrDefault()
	apiSrv := api.New(mcs)
	addr := net.JoinHostPort(bind, strconv.Itoa(port))
	apiLis, apiErr := net.Listen("tcp", addr)
	if apiErr != nil {
		fmt.Fprintf(stderr, "gc supervisor: api: listen %s failed: %v\n", addr, apiErr) //nolint:errcheck
		// Non-fatal — continue without API.
	} else {
		go func() {
			if err := apiSrv.Serve(apiLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintf(stderr, "gc supervisor: api: %v\n", err) //nolint:errcheck
			}
		}()
		defer func() {
			shutCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
			apiSrv.Shutdown(shutCtx) //nolint:errcheck
		}()
		fmt.Fprintf(stdout, "Supervisor API listening on http://%s\n", addr) //nolint:errcheck
	}

	fmt.Fprintln(stdout, "Supervisor started.") //nolint:errcheck

	// Reconciliation loop.
	interval := supCfg.Supervisor.PatrolIntervalDuration()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Initial reconcile.
	reconcileCities(reg, cities, &mu, stdout, stderr)

	for {
		select {
		case <-ticker.C:
			reconcileCities(reg, cities, &mu, stdout, stderr)
		case <-ctx.Done():
			// Shutdown all cities.
			mu.Lock()
			for name, mc := range cities {
				fmt.Fprintf(stdout, "Stopping city '%s'...\n", name) //nolint:errcheck
				mc.cancel()
				<-mc.done
				mc.cr.shutdown()
				fmt.Fprintf(stdout, "City '%s' stopped.\n", name) //nolint:errcheck
			}
			mu.Unlock()
			fmt.Fprintln(stdout, "Supervisor stopped.") //nolint:errcheck
			return 0
		}
	}
}

// reconcileCities compares the registry against running cities and
// starts/stops as needed.
func reconcileCities(
	reg *supervisor.Registry,
	cities map[string]*managedCity,
	mu *sync.Mutex,
	stdout, stderr io.Writer,
) {
	entries, err := reg.List()
	if err != nil {
		fmt.Fprintf(stderr, "gc supervisor: registry: %v\n", err) //nolint:errcheck
		return
	}

	// Build desired set.
	desired := make(map[string]supervisor.CityEntry, len(entries))
	for _, e := range entries {
		desired[e.Path] = e
	}

	mu.Lock()
	defer mu.Unlock()

	// Stop cities no longer in registry.
	for path, mc := range cities {
		if _, ok := desired[path]; !ok {
			name := filepath.Base(path)
			fmt.Fprintf(stdout, "Unregistered city '%s', stopping...\n", name) //nolint:errcheck
			mc.cancel()
			<-mc.done
			mc.cr.shutdown()
			delete(cities, path)
			fmt.Fprintf(stdout, "City '%s' stopped.\n", name) //nolint:errcheck
		}
	}

	// Start new cities.
	for path, entry := range desired {
		if _, running := cities[path]; running {
			continue
		}
		name := entry.Name()

		// Load city config.
		tomlPath := filepath.Join(path, "city.toml")
		cfg, loadErr := config.Load(fsys.OSFS{}, tomlPath)
		if loadErr != nil {
			fmt.Fprintf(stderr, "gc supervisor: city '%s': %v (skipping)\n", name, loadErr) //nolint:errcheck
			continue
		}

		cityName := cfg.Workspace.Name
		if cityName == "" {
			cityName = filepath.Base(path)
		}

		// Warn if city has its own API port.
		if cfg.API.Port > 0 {
			fmt.Fprintf(stderr, "gc supervisor: city '%s' has [api] port=%d which is ignored under supervisor mode\n", //nolint:errcheck
				cityName, cfg.API.Port)
		}

		sp, spErr := newSessionProviderByName(
			effectiveProviderName(cfg.Session.Provider), cfg.Session, cityName)
		if spErr != nil {
			fmt.Fprintf(stderr, "gc supervisor: city '%s': session provider: %v (skipping)\n", name, spErr) //nolint:errcheck
			continue
		}

		rec := events.Discard
		var eventProv events.Provider
		evPath := filepath.Join(path, ".gc", "events.jsonl")
		if fr, frErr := events.NewFileRecorder(evPath, stderr); frErr == nil {
			rec = fr
			eventProv = fr
		}

		dops := newDrainOps(sp)
		poolSessions := computePoolSessions(cfg, cityName, sp)
		poolDeathHandlers := computePoolDeathHandlers(cfg, cityName, path, sp)
		watchDirs := config.WatchDirs(nil, cfg, path)

		cr := newCityRuntime(CityRuntimeParams{
			CityPath:          path,
			CityName:          cityName,
			TomlPath:          tomlPath,
			WatchDirs:         watchDirs,
			Cfg:               cfg,
			SP:                sp,
			BuildFn:           supervisorBuildAgentsFn(path, cityName, stderr),
			Dops:              dops,
			Rec:               rec,
			PoolSessions:      poolSessions,
			PoolDeathHandlers: poolDeathHandlers,
			Stdout:            stdout,
			Stderr:            stderr,
		})

		// Wire API state.
		cs := newControllerState(cfg, sp, eventProv, cityName, path)
		cs.ct = cr.crashTrack()
		cr.setControllerState(cs)

		cityCtx, cityCancel := context.WithCancel(context.Background())
		done := make(chan struct{})

		go func(n string) {
			defer close(done)
			defer func() {
				if r := recover(); r != nil {
					fmt.Fprintf(stderr, "gc supervisor: city '%s' panicked: %v\n", n, r) //nolint:errcheck
				}
			}()
			cr.run(cityCtx)
		}(cityName)

		cities[path] = &managedCity{cr: cr, cancel: cityCancel, done: done}
		rec.Record(events.Event{Type: events.ControllerStarted, Actor: "gc"})
		fmt.Fprintf(stdout, "Started city '%s' (%s)\n", cityName, path) //nolint:errcheck
	}
}

// effectiveProviderName returns the provider name respecting GC_SESSION env override.
func effectiveProviderName(configured string) string {
	if v := os.Getenv("GC_SESSION"); v != "" {
		return v
	}
	return configured
}

// supervisorBuildAgentsFn returns a buildFn suitable for CityRuntimeParams.
// This mirrors the buildAgents closure in cmd_start.go but without dynamic
// pool evaluation (pools use their static min count).
func supervisorBuildAgentsFn(cityPath, cityName string, stderr io.Writer) func(*config.City, session.Provider) []agent.Agent {
	return func(c *config.City, sp session.Provider) []agent.Agent {
		bp := newAgentBuildParams(cityName, cityPath, c, sp, time.Now(), stderr)
		var agents []agent.Agent
		for i := range c.Agents {
			a := &c.Agents[i]
			pool := a.EffectivePool()
			if pool.IsMultiInstance() {
				pa, err := poolAgents(bp, a, pool.Min)
				if err != nil {
					fmt.Fprintf(stderr, "gc supervisor: pool %q: %v (skipping)\n", a.QualifiedName(), err) //nolint:errcheck
					continue
				}
				agents = append(agents, pa...)
				continue
			}
			qualifiedName := a.QualifiedName()
			built, err := buildOneAgent(bp, a, qualifiedName, nil)
			if err != nil {
				fmt.Fprintf(stderr, "gc supervisor: agent %q: %v (skipping)\n", qualifiedName, err) //nolint:errcheck
				continue
			}
			agents = append(agents, built)
		}
		return agents
	}
}

// multiCityState implements api.State by delegating to the first registered
// city's controllerState. This is the v0 compatibility behavior described
// in the design doc — Phase 2 adds proper city-namespaced routing.
type multiCityState struct {
	cities    map[string]*managedCity
	mu        *sync.Mutex
	startedAt time.Time
}

// firstCity returns the controllerState of the first city, or nil.
func (m *multiCityState) firstCity() *controllerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, mc := range m.cities {
		if mc.cr.cs != nil {
			return mc.cr.cs
		}
	}
	return nil
}

// --- api.State implementation (delegates to first city) ---

func (m *multiCityState) Config() *config.City {
	if cs := m.firstCity(); cs != nil {
		return cs.Config()
	}
	return &config.City{}
}

func (m *multiCityState) SessionProvider() session.Provider {
	if cs := m.firstCity(); cs != nil {
		return cs.SessionProvider()
	}
	return nil
}

func (m *multiCityState) BeadStore(rig string) beads.Store {
	if cs := m.firstCity(); cs != nil {
		return cs.BeadStore(rig)
	}
	return nil
}

func (m *multiCityState) BeadStores() map[string]beads.Store {
	if cs := m.firstCity(); cs != nil {
		return cs.BeadStores()
	}
	return nil
}

func (m *multiCityState) MailProvider(rig string) mail.Provider {
	if cs := m.firstCity(); cs != nil {
		return cs.MailProvider(rig)
	}
	return nil
}

func (m *multiCityState) MailProviders() map[string]mail.Provider {
	if cs := m.firstCity(); cs != nil {
		return cs.MailProviders()
	}
	return nil
}

func (m *multiCityState) EventProvider() events.Provider {
	if cs := m.firstCity(); cs != nil {
		return cs.EventProvider()
	}
	return nil
}

func (m *multiCityState) CityName() string {
	if cs := m.firstCity(); cs != nil {
		return cs.CityName()
	}
	return ""
}

func (m *multiCityState) CityPath() string {
	if cs := m.firstCity(); cs != nil {
		return cs.CityPath()
	}
	return ""
}

func (m *multiCityState) Version() string {
	return version
}

func (m *multiCityState) StartedAt() time.Time {
	return m.startedAt
}

func (m *multiCityState) IsQuarantined(sessionName string) bool {
	if cs := m.firstCity(); cs != nil {
		return cs.IsQuarantined(sessionName)
	}
	return false
}
