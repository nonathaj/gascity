package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newAgentStartCmd creates the compatibility "gc agent start <template>" command.
func newAgentStartCmd(stdout, stderr io.Writer) *cobra.Command {
	var nameFlag string
	cmd := &cobra.Command{
		Use:   "start <template>",
		Short: "Create a background session from an agent template",
		Long: `Create a detached session from an agent template.

This is a compatibility alias for "gc session new <template> --no-attach".
Templates can always be used as session sources.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdAgentStart(args[0], nameFlag, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&nameFlag, "name", "", "deprecated compatibility alias for --title")
	return cmd
}

func cmdAgentStart(templateInput, nameFlag string, stdout, stderr io.Writer) int {
	if nameFlag != "" {
		fmt.Fprintln(stderr, "gc agent start: --name is deprecated; using it as the session title") //nolint:errcheck // best-effort stderr
	}
	return cmdSessionNew([]string{templateInput}, nameFlag, true, stdout, stderr)
}

// newAgentStopCmd creates the compatibility "gc agent stop <session-id-or-name>" command.
func newAgentStopCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "stop <session-id-or-name>",
		Short: "Suspend a session created from an agent template",
		Long: `Suspend a running session and keep its conversation state.

This is a compatibility alias for "gc session suspend". When multiple
sessions exist for the same template, pass a session ID.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdAgentStop(args[0], stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
}

func cmdAgentStop(input string, stdout, stderr io.Writer) int {
	return cmdSessionSuspend([]string{input}, stdout, stderr)
}

// newAgentDestroyCmd creates the compatibility "gc agent destroy <session-id-or-name>" command.
func newAgentDestroyCmd(stdout, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy <session-id-or-name>",
		Short: "Close a session created from an agent template",
		Long: `Close a session permanently.

This is a compatibility alias for "gc session close". When multiple
sessions exist for the same template, pass a session ID.`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdAgentDestroy(args[0], stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
}

func cmdAgentDestroy(input string, stdout, stderr io.Writer) int {
	return cmdSessionClose([]string{input}, stdout, stderr)
}
