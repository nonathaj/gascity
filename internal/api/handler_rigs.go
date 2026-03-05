package api

import (
	"net/http"
	"strings"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/session"
)

type rigResponse struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Suspended bool   `json:"suspended"`
	Prefix    string `json:"prefix,omitempty"`
}

func (s *Server) handleRigList(w http.ResponseWriter, r *http.Request) {
	bp := parseBlockingParams(r)
	if bp.isBlocking() {
		waitForChange(r.Context(), s.state.EventProvider(), bp)
	}

	cfg := s.state.Config()
	sp := s.state.SessionProvider()
	rigs := make([]rigResponse, 0, len(cfg.Rigs))
	for _, rig := range cfg.Rigs {
		rigs = append(rigs, rigResponse{
			Name:      rig.Name,
			Path:      rig.Path,
			Suspended: rigSuspended(cfg, rig, sp, s.state.CityName()),
			Prefix:    rig.Prefix,
		})
	}
	writeListJSON(w, s.latestIndex(), rigs, len(rigs))
}

func (s *Server) handleRig(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg := s.state.Config()
	sp := s.state.SessionProvider()

	for _, rig := range cfg.Rigs {
		if rig.Name == name {
			writeIndexJSON(w, s.latestIndex(), rigResponse{
				Name:      rig.Name,
				Path:      rig.Path,
				Suspended: rigSuspended(cfg, rig, sp, s.state.CityName()),
				Prefix:    rig.Prefix,
			})
			return
		}
	}
	writeError(w, http.StatusNotFound, "not_found", "rig "+name+" not found")
}

func (s *Server) handleRigAction(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	action := r.PathValue("action")

	sm, ok := s.state.(StateMutator)
	if !ok {
		writeError(w, http.StatusNotImplemented, "internal", "mutations not supported")
		return
	}

	var err error
	switch action {
	case "suspend":
		err = sm.SuspendRig(name)
	case "resume":
		err = sm.ResumeRig(name)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown rig action: "+action)
		return
	}

	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, "not_found", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "action": action, "rig": name})
}

// rigSuspended computes effective suspended state for a rig by merging config
// and runtime session metadata. A rig is suspended if the config says so, or
// if all its agents are runtime-suspended via session metadata.
func rigSuspended(cfg *config.City, rig config.Rig, sp session.Provider, cityName string) bool {
	if rig.Suspended {
		return true
	}
	tmpl := cfg.Workspace.SessionTemplate
	var agentCount, suspendedCount int
	for _, a := range cfg.Agents {
		if a.Dir != rig.Name {
			continue
		}
		expanded := expandAgent(a, cityName)
		for _, ea := range expanded {
			agentCount++
			sessionName := agent.SessionNameFor(cityName, ea.qualifiedName, tmpl)
			if v, err := sp.GetMeta(sessionName, "suspended"); err == nil && v == "true" {
				suspendedCount++
			}
		}
	}
	return agentCount > 0 && suspendedCount == agentCount
}
