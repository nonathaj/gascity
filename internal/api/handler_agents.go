package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/julianknutsen/gascity/internal/agent"
	"github.com/julianknutsen/gascity/internal/config"
)

type agentResponse struct {
	Name       string       `json:"name"`
	Running    bool         `json:"running"`
	Suspended  bool         `json:"suspended"`
	Rig        string       `json:"rig,omitempty"`
	Pool       string       `json:"pool,omitempty"`
	Session    *sessionInfo `json:"session,omitempty"`
	ActiveBead string       `json:"active_bead,omitempty"`
}

type sessionInfo struct {
	Name         string     `json:"name"`
	LastActivity *time.Time `json:"last_activity,omitempty"`
}

func (s *Server) handleAgentList(w http.ResponseWriter, r *http.Request) {
	bp := parseBlockingParams(r)
	if bp.isBlocking() {
		waitForChange(r.Context(), s.state.EventProvider(), bp)
	}

	cfg := s.state.Config()
	sp := s.state.SessionProvider()
	cityName := s.state.CityName()
	sessTmpl := cfg.Workspace.SessionTemplate

	// Query filters.
	qPool := r.URL.Query().Get("pool")
	qRig := r.URL.Query().Get("rig")
	qRunning := r.URL.Query().Get("running")

	var agents []agentResponse
	for _, a := range cfg.Agents {
		expanded := expandAgent(a, cityName)
		for _, ea := range expanded {
			// Apply filters.
			if qRig != "" && ea.rig != qRig {
				continue
			}
			if qPool != "" && ea.pool != qPool {
				continue
			}

			sessionName := agentSessionName(cityName, ea.qualifiedName, sessTmpl)
			running := sp.IsRunning(sessionName)

			if qRunning == "true" && !running {
				continue
			}
			if qRunning == "false" && running {
				continue
			}

			// Merge config + runtime suspended state.
			suspended := ea.suspended
			if v, err := sp.GetMeta(sessionName, "suspended"); err == nil && v == "true" {
				suspended = true
			}

			resp := agentResponse{
				Name:      ea.qualifiedName,
				Running:   running,
				Suspended: suspended,
				Rig:       ea.rig,
				Pool:      ea.pool,
			}

			if running {
				si := &sessionInfo{Name: sessionName}
				if t, err := sp.GetLastActivity(sessionName); err == nil && !t.IsZero() {
					si.LastActivity = &t
				}
				resp.Session = si
			}

			// Check for active bead via session metadata.
			if hook, err := sp.GetMeta(sessionName, "hook"); err == nil && hook != "" {
				resp.ActiveBead = hook
			}

			agents = append(agents, resp)
		}
	}

	if agents == nil {
		agents = []agentResponse{}
	}
	writeListJSON(w, s.latestIndex(), agents, len(agents))
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "invalid", "agent name required")
		return
	}

	cfg := s.state.Config()
	sp := s.state.SessionProvider()
	cityName := s.state.CityName()

	// Try exact agent match first, then check for sub-resource suffix.
	// This prevents agent names ending in "/peek" from being misrouted.
	agentCfg, ok := findAgent(cfg, name)
	if !ok {
		// Not found as exact agent — check for /peek sub-resource.
		if after, found := strings.CutSuffix(name, "/peek"); found {
			s.handleAgentPeek(w, r, after)
			return
		}
		writeError(w, http.StatusNotFound, "not_found", "agent "+name+" not found")
		return
	}

	sessionName := agentSessionName(cityName, name, cfg.Workspace.SessionTemplate)
	running := sp.IsRunning(sessionName)

	// Merge config + runtime suspended state.
	suspended := agentCfg.Suspended
	if v, err := sp.GetMeta(sessionName, "suspended"); err == nil && v == "true" {
		suspended = true
	}

	resp := agentResponse{
		Name:      name,
		Running:   running,
		Suspended: suspended,
		Rig:       agentCfg.Dir,
	}
	if agentCfg.IsPool() {
		resp.Pool = agentCfg.QualifiedName()
	}

	if running {
		si := &sessionInfo{Name: sessionName}
		if t, err := sp.GetLastActivity(sessionName); err == nil && !t.IsZero() {
			si.LastActivity = &t
		}
		resp.Session = si
	}

	if hook, err := sp.GetMeta(sessionName, "hook"); err == nil && hook != "" {
		resp.ActiveBead = hook
	}

	writeIndexJSON(w, s.latestIndex(), resp)
}

func (s *Server) handleAgentPeek(w http.ResponseWriter, _ *http.Request, name string) {
	sp := s.state.SessionProvider()
	cfg := s.state.Config()
	sessionName := agentSessionName(s.state.CityName(), name, cfg.Workspace.SessionTemplate)

	if !sp.IsRunning(sessionName) {
		writeError(w, http.StatusNotFound, "not_found", "agent "+name+" not running")
		return
	}

	output, err := sp.Peek(sessionName, 100)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"output": output})
}

func (s *Server) handleAgentAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	sm, ok := s.state.(StateMutator)
	if !ok {
		writeError(w, http.StatusNotImplemented, "internal", "mutations not supported")
		return
	}

	// Parse action suffix before validating agent name.
	var action string
	if after, found := strings.CutSuffix(name, "/suspend"); found {
		name = after
		action = "suspend"
	} else if after, found := strings.CutSuffix(name, "/resume"); found {
		name = after
		action = "resume"
	} else if after, found := strings.CutSuffix(name, "/kill"); found {
		name = after
		action = "kill"
	} else if after, found := strings.CutSuffix(name, "/drain"); found {
		name = after
		action = "drain"
	} else if after, found := strings.CutSuffix(name, "/undrain"); found {
		name = after
		action = "undrain"
	} else if after, found := strings.CutSuffix(name, "/nudge"); found {
		name = after
		action = "nudge"
	} else {
		writeError(w, http.StatusNotFound, "not_found", "unknown agent action")
		return
	}

	// Validate agent exists in config.
	cfg := s.state.Config()
	agentCfg, ok := findAgent(cfg, name)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "agent "+name+" not found")
		return
	}

	// Reject mutations on the pool parent when max > 1.
	// Runtime sessions are pool-1, pool-2, etc. — mutating the parent is a no-op.
	if agentCfg.IsPool() {
		pool := agentCfg.EffectivePool()
		if pool.Max > 1 && name == agentCfg.QualifiedName() {
			writeError(w, http.StatusBadRequest, "invalid",
				"cannot mutate pool parent "+name+"; target a specific member (e.g. "+name+"-1)")
			return
		}
	}

	var err error
	switch action {
	case "suspend":
		err = sm.SuspendAgent(name)
	case "resume":
		err = sm.ResumeAgent(name)
	case "kill":
		err = sm.KillAgent(name)
	case "drain":
		err = sm.DrainAgent(name)
	case "undrain":
		err = sm.UndrainAgent(name)
	case "nudge":
		var body struct {
			Message string `json:"message"`
		}
		if decErr := decodeBody(r, &body); decErr != nil {
			writeError(w, http.StatusBadRequest, "invalid", decErr.Error())
			return
		}
		err = sm.NudgeAgent(name, body.Message)
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// expandedAgent holds a single (possibly pool-expanded) agent identity.
type expandedAgent struct {
	qualifiedName string
	rig           string
	pool          string
	suspended     bool
}

// expandAgent expands a config.Agent into its effective runtime agents.
// For pool agents, this generates pool-1, pool-2, ..., pool-max members.
func expandAgent(a config.Agent, _ string) []expandedAgent {
	if !a.IsPool() {
		return []expandedAgent{{
			qualifiedName: a.QualifiedName(),
			rig:           a.Dir,
			suspended:     a.Suspended,
		}}
	}

	pool := a.EffectivePool()
	poolMax := pool.Max
	if poolMax <= 0 {
		poolMax = 1
	}
	poolName := a.QualifiedName()

	var result []expandedAgent
	for i := 1; i <= poolMax; i++ {
		memberName := a.Name
		if poolMax > 1 {
			memberName = a.Name + "-" + strconv.Itoa(i)
		}
		qn := memberName
		if a.Dir != "" {
			qn = a.Dir + "/" + memberName
		}
		result = append(result, expandedAgent{
			qualifiedName: qn,
			rig:           a.Dir,
			pool:          poolName,
			suspended:     a.Suspended,
		})
	}
	return result
}

// agentSessionName converts a qualified agent name to a tmux session name
// using the canonical naming contract from agent.SessionNameFor.
func agentSessionName(cityName, qualifiedName, sessionTemplate string) string {
	return agent.SessionNameFor(cityName, qualifiedName, sessionTemplate)
}

// findAgent looks up an agent by qualified name in the config.
// For pool members, it matches the pool definition.
func findAgent(cfg *config.City, name string) (config.Agent, bool) {
	dir, baseName := config.ParseQualifiedName(name)
	for _, a := range cfg.Agents {
		if a.Dir == dir && a.Name == baseName {
			return a, true
		}
		// Check pool members: strip numeric suffix.
		if a.IsPool() && a.Dir == dir {
			pool := a.EffectivePool()
			poolMax := pool.Max
			if poolMax <= 0 {
				poolMax = 1
			}
			for i := 1; i <= poolMax; i++ {
				memberName := a.Name
				if poolMax > 1 {
					memberName = a.Name + "-" + strconv.Itoa(i)
				}
				if memberName == baseName {
					return a, true
				}
			}
		}
	}
	return config.Agent{}, false
}
