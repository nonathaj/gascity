package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/gastownhall/gascity/internal/agent"
	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/gastownhall/gascity/internal/session"
)

func ensureSessionForTemplate(
	cityPath string,
	cfg *config.City,
	store beads.Store,
	templateName string,
	stderr io.Writer,
) (string, error) {
	if store == nil {
		return "", fmt.Errorf("session store unavailable for template %q", templateName)
	}
	if sn, ok := lookupSessionName(store, templateName); ok {
		return sn, nil
	}
	if cfg == nil {
		return agent.SessionNameFor("", templateName, ""), nil
	}

	found, ok := resolveSessionTemplate(cfg, templateName, graphRouteRigContext(templateName))
	if !ok {
		return agent.SessionNameFor(cfg.Workspace.Name, templateName, cfg.Workspace.SessionTemplate), nil
	}
	resolved, err := config.ResolveProvider(&found, &cfg.Workspace, cfg.Providers, exec.LookPath)
	if err != nil {
		return "", err
	}
	workDir, err := resolveWorkDir(cityPath, cfg, &found)
	if err != nil {
		return "", err
	}

	sp := newSessionProvider()
	mgr := newSessionManager(store, sp)
	title := found.QualifiedName()
	resume := session.ProviderResume{
		ResumeFlag:    resolved.ResumeFlag,
		ResumeStyle:   resolved.ResumeStyle,
		ResumeCommand: resolved.ResumeCommand,
		SessionIDFlag: resolved.SessionIDFlag,
	}

	if pokeErr := pokeController(cityPath); pokeErr == nil {
		info, createErr := mgr.CreateBeadOnly(
			found.QualifiedName(),
			title,
			resolved.CommandString(),
			workDir,
			resolved.Name,
			found.Session,
			resolved.Env,
			resume,
		)
		if createErr == nil {
			_ = pokeController(cityPath)
			return info.SessionName, nil
		}
		if sn, ok := lookupSessionName(store, found.QualifiedName()); ok {
			return sn, nil
		}
		return "", createErr
	}

	hints := runtime.Config{
		ReadyPromptPrefix:      resolved.ReadyPromptPrefix,
		ReadyDelayMs:           resolved.ReadyDelayMs,
		ProcessNames:           resolved.ProcessNames,
		EmitsPermissionWarning: resolved.EmitsPermissionWarning,
	}
	info, err := mgr.CreateWithTransport(
		context.Background(),
		found.QualifiedName(),
		title,
		resolved.CommandString(),
		workDir,
		resolved.Name,
		found.Session,
		resolved.Env,
		resume,
		hints,
	)
	if err == nil {
		return info.SessionName, nil
	}
	if sn, ok := lookupSessionName(store, found.QualifiedName()); ok {
		return sn, nil
	}
	return "", err
}
