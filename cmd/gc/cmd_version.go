package main

import (
	"fmt"
	"io"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Build metadata — injected via ldflags at build time.
// Falls back to VCS info embedded by the Go toolchain (go install, go build).
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func init() {
	if commit != "unknown" {
		return // ldflags provided version info — don't override.
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}
	var dirty bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if s.Value != "" {
				if len(s.Value) > 12 {
					commit = s.Value[:12]
				} else {
					commit = s.Value
				}
			}
		case "vcs.time":
			if date == "unknown" && s.Value != "" {
				date = s.Value
			}
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if dirty && commit != "unknown" {
		commit += "-dirty"
	}
}

func newVersionCmd(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print gc version information",
		Long: `Print gc version, git commit, and build date.

Version information is injected via ldflags at build time.
When built with go install, VCS metadata is read from the binary.`,
		Args: cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Fprintf(stdout, "gc %s (commit: %s, built: %s)\n", version, commit, date) //nolint:errcheck // best-effort stdout
		},
	}
}
