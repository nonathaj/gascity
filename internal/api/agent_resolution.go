package api

import (
	"strconv"
	"strings"

	"github.com/gastownhall/gascity/internal/config"
)

// resolveSessionTemplateAgent mirrors CLI agent identity resolution except for
// cwd-based rig context, which HTTP requests do not have. It accepts exact
// qualified names, pool members, multi instances, and unambiguous bare names.
func resolveSessionTemplateAgent(cfg *config.City, input string) (config.Agent, bool) {
	if a, ok := findAgentByIdentity(cfg, input); ok {
		return a, true
	}
	if strings.Contains(input, "/") {
		return config.Agent{}, false
	}

	var matches []config.Agent
	for _, a := range cfg.Agents {
		if a.Name == input {
			matches = append(matches, a)
			continue
		}
		if a.Pool != nil && a.Pool.IsMultiInstance() {
			prefix := a.Name + "-"
			if !strings.HasPrefix(input, prefix) {
				continue
			}
			suffix := input[len(prefix):]
			n, err := strconv.Atoi(suffix)
			if err != nil || n < 1 || (!a.Pool.IsUnlimited() && n > a.Pool.Max) {
				continue
			}
			instance := a
			instance.Name = input
			instance.Pool = nil
			matches = append(matches, instance)
		}
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return config.Agent{}, false
}

func findAgentByIdentity(cfg *config.City, identity string) (config.Agent, bool) {
	dir, name := config.ParseQualifiedName(identity)
	for _, a := range cfg.Agents {
		if a.Dir == dir && a.Name == name {
			return a, true
		}
		if a.Dir == dir && a.Pool != nil && a.Pool.IsMultiInstance() {
			prefix := a.Name + "-"
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			suffix := name[len(prefix):]
			n, err := strconv.Atoi(suffix)
			if err != nil || n < 1 || (!a.Pool.IsUnlimited() && n > a.Pool.Max) {
				continue
			}
			instance := a
			instance.Name = name
			instance.Pool = nil
			return instance, true
		}
	}

	for _, a := range cfg.Agents {
		if !a.IsMulti() {
			continue
		}
		templateQN := a.QualifiedName()
		prefix := templateQN + "/"
		if !strings.HasPrefix(identity, prefix) {
			continue
		}
		instanceName := identity[len(prefix):]
		if instanceName == "" {
			continue
		}
		instance := a
		instance.Name = instanceName
		instance.Multi = false
		instance.PoolName = templateQN
		return instance, true
	}
	return config.Agent{}, false
}
