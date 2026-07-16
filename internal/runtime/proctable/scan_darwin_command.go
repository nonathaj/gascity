package proctable

import "strings"

func darwinPSCommand(fields []string) string {
	if len(fields) < 3 {
		return ""
	}
	return fields[2]
}

func isInfrastructureCommand(command string) bool {
	lower := strings.ToLower(command)
	// psmux is the Windows tmux workalike; its name does not contain the
	// substring "tmux", so it needs its own match.
	return strings.Contains(lower, "tmux") || strings.Contains(lower, "psmux")
}
