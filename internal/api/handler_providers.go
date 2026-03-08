package api

import (
	"net/http"
	"sort"

	"github.com/gastownhall/gascity/internal/config"
)

type providerResponse struct {
	Name         string            `json:"name"`
	DisplayName  string            `json:"display_name,omitempty"`
	Command      string            `json:"command,omitempty"`
	Args         []string          `json:"args,omitempty"`
	PromptMode   string            `json:"prompt_mode,omitempty"`
	PromptFlag   string            `json:"prompt_flag,omitempty"`
	ReadyDelayMs int               `json:"ready_delay_ms,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Builtin      bool              `json:"builtin"`
	CityLevel    bool              `json:"city_level"`
}

func providerFromSpec(name string, spec config.ProviderSpec, builtin, cityLevel bool) providerResponse {
	return providerResponse{
		Name:         name,
		DisplayName:  spec.DisplayName,
		Command:      spec.Command,
		Args:         spec.Args,
		PromptMode:   spec.PromptMode,
		PromptFlag:   spec.PromptFlag,
		ReadyDelayMs: spec.ReadyDelayMs,
		Env:          spec.Env,
		Builtin:      builtin,
		CityLevel:    cityLevel,
	}
}

func (s *Server) handleProviderList(w http.ResponseWriter, _ *http.Request) {
	cfg := s.state.Config()
	builtins := config.BuiltinProviders()
	builtinOrder := config.BuiltinProviderOrder()

	// Collect all providers: city-level overrides + builtins.
	seen := make(map[string]bool)
	var providers []providerResponse

	// City-level providers first (sorted alphabetically).
	var cityNames []string
	for name := range cfg.Providers {
		cityNames = append(cityNames, name)
	}
	sort.Strings(cityNames)
	for _, name := range cityNames {
		spec := cfg.Providers[name]
		_, isBuiltin := builtins[name]
		providers = append(providers, providerFromSpec(name, spec, isBuiltin, true))
		seen[name] = true
	}

	// Builtins not overridden by city-level (in canonical order).
	for _, name := range builtinOrder {
		if seen[name] {
			continue
		}
		providers = append(providers, providerFromSpec(name, builtins[name], true, false))
	}

	writeListJSON(w, s.latestIndex(), providers, len(providers))
}

func (s *Server) handleProviderGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	cfg := s.state.Config()
	builtins := config.BuiltinProviders()

	// Check city-level first.
	if spec, ok := cfg.Providers[name]; ok {
		_, isBuiltin := builtins[name]
		writeIndexJSON(w, s.latestIndex(), providerFromSpec(name, spec, isBuiltin, true))
		return
	}

	// Check builtins.
	if spec, ok := builtins[name]; ok {
		writeIndexJSON(w, s.latestIndex(), providerFromSpec(name, spec, true, false))
		return
	}

	writeError(w, http.StatusNotFound, "not_found", "provider "+name+" not found")
}
