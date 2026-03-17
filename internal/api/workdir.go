package api

import (
	"bytes"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/gastownhall/gascity/internal/config"
)

type agentPathContext struct {
	Agent     string
	AgentBase string
	Rig       string
	RigRoot   string
	CityRoot  string
	CityName  string
}

func configuredRigName(cityPath string, cfg *config.City, a config.Agent) string {
	if cfg == nil || a.Dir == "" {
		return ""
	}
	for _, rig := range cfg.Rigs {
		if a.Dir == rig.Name {
			return rig.Name
		}
	}
	abs := resolveDirPath(cityPath, a.Dir)
	for _, rig := range cfg.Rigs {
		if filepath.Clean(abs) == filepath.Clean(rig.Path) {
			return rig.Name
		}
	}
	return ""
}

func rigRootForName(cfg *config.City, rigName string) string {
	if cfg == nil {
		return ""
	}
	for _, rig := range cfg.Rigs {
		if rig.Name == rigName {
			return rig.Path
		}
	}
	return ""
}

func effectiveWorkDirSpec(a config.Agent) string {
	return a.WorkDir
}

func resolveDirPath(cityPath, dir string) string {
	if dir == "" {
		return cityPath
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(cityPath, dir)
}

func expandDirTemplate(dir string, ctx agentPathContext) string {
	if dir == "" || !strings.Contains(dir, "{{") {
		return dir
	}
	tmpl, err := template.New("dir").Parse(dir)
	if err != nil {
		return dir
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, ctx); err != nil {
		return dir
	}
	return buf.String()
}

func agentPathContextForName(cityPath string, cfg *config.City, a config.Agent, qualifiedName string) agentPathContext {
	rigName := configuredRigName(cityPath, cfg, a)
	_, agentBase := config.ParseQualifiedName(qualifiedName)
	return agentPathContext{
		Agent:     qualifiedName,
		AgentBase: agentBase,
		Rig:       rigName,
		RigRoot:   rigRootForName(cfg, rigName),
		CityRoot:  cityPath,
		CityName:  cfg.Workspace.Name,
	}
}

func resolveAgentWorkDirForName(cityPath string, cfg *config.City, a config.Agent, qualifiedName string) string {
	if cfg == nil {
		return resolveDirPath(cityPath, "")
	}
	if a.WorkDir == "" {
		if rigName := configuredRigName(cityPath, cfg, a); rigName != "" {
			if rigRoot := rigRootForName(cfg, rigName); rigRoot != "" {
				return resolveDirPath(cityPath, rigRoot)
			}
		}
		return resolveDirPath(cityPath, a.Dir)
	}
	ctx := agentPathContextForName(cityPath, cfg, a, qualifiedName)
	return resolveDirPath(cityPath, expandDirTemplate(effectiveWorkDirSpec(a), ctx))
}
