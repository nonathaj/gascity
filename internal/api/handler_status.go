package api

import (
	"net/http"
)

type statusResponse struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	AgentCount int    `json:"agent_count"`
	RigCount   int    `json:"rig_count"`
	Running    int    `json:"running"`
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	bp := parseBlockingParams(r)
	if bp.isBlocking() {
		waitForChange(r.Context(), s.state.EventProvider(), bp)
	}

	cfg := s.state.Config()
	sp := s.state.SessionProvider()
	cityName := s.state.CityName()
	sessTmpl := cfg.Workspace.SessionTemplate

	// Count running agents by checking each configured agent's canonical session name.
	var running int
	for _, a := range cfg.Agents {
		for _, ea := range expandAgent(a, cityName) {
			sessName := agentSessionName(cityName, ea.qualifiedName, sessTmpl)
			if sp.IsRunning(sessName) {
				running++
			}
		}
	}

	// Count effective agents (including expanded pool members).
	var agentCount int
	for _, a := range cfg.Agents {
		agentCount += len(expandAgent(a, cityName))
	}

	resp := statusResponse{
		Name:       cityName,
		Path:       s.state.CityPath(),
		AgentCount: agentCount,
		RigCount:   len(cfg.Rigs),
		Running:    running,
	}
	writeIndexJSON(w, s.latestIndex(), resp)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
