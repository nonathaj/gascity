package doctor

import (
	"errors"
	"os/exec"
	"strings"
)

// TopologyScriptCheck implements Check by running a script shipped with
// a topology. The script follows the topology doctor protocol:
//
//   - Exit 0 = OK, Exit 1 = Warning, Exit 2 = Error
//   - First line of stdout = message (shown after check name)
//   - Remaining stdout lines = details (shown in verbose mode)
//
// The script receives environment variables:
//
//	GC_CITY_PATH    — absolute path to the city root
//	GC_TOPOLOGY_DIR — absolute path to the topology directory
type TopologyScriptCheck struct {
	// CheckName is the fully-qualified name, e.g. "maintenance:check-binaries".
	CheckName string
	// Script is the absolute path to the check script.
	Script string
	// TopologyDir is the absolute topology directory path.
	TopologyDir string
}

// Name returns the check's fully-qualified name.
func (c *TopologyScriptCheck) Name() string { return c.CheckName }

// CanFix reports that topology script checks do not support auto-fix.
func (c *TopologyScriptCheck) CanFix() bool { return false }

// Fix is a no-op (topology script checks do not support auto-fix).
func (c *TopologyScriptCheck) Fix(_ *CheckContext) error { return nil }

// Run executes the topology script and interprets its output.
func (c *TopologyScriptCheck) Run(ctx *CheckContext) *CheckResult {
	cmd := exec.Command(c.Script) //nolint:gosec // script path from topology config
	cmd.Dir = c.TopologyDir
	cmd.Env = append(cmd.Environ(),
		"GC_CITY_PATH="+ctx.CityPath,
		"GC_TOPOLOGY_DIR="+c.TopologyDir,
	)

	out, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// Script not found or not executable.
			return &CheckResult{
				Name:    c.CheckName,
				Status:  StatusError,
				Message: "script error: " + err.Error(),
			}
		}
	}

	message, details := parseScriptOutput(string(out))
	if message == "" {
		message = "check completed"
	}

	var status CheckStatus
	switch exitCode {
	case 0:
		status = StatusOK
	case 1:
		status = StatusWarning
	default:
		status = StatusError
	}

	return &CheckResult{
		Name:    c.CheckName,
		Status:  status,
		Message: message,
		Details: details,
	}
}

// parseScriptOutput splits script output into a message (first line)
// and details (remaining non-empty lines).
func parseScriptOutput(output string) (string, []string) {
	output = strings.TrimSpace(output)
	if output == "" {
		return "", nil
	}

	lines := strings.Split(output, "\n")
	message := strings.TrimSpace(lines[0])

	var details []string
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			details = append(details, trimmed)
		}
	}
	return message, details
}
