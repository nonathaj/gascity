package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

func newAgentStartCmd(_, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Deprecated: use \"gc session new\"",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprintln(stderr, "gc agent start: removed, use \"gc session new\" instead") //nolint:errcheck // best-effort stderr
			return errExit
		},
	}
}

func newAgentStopCmd(_, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Deprecated: use \"gc session suspend\"",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprintln(stderr, "gc agent stop: removed, use \"gc session suspend\" instead") //nolint:errcheck // best-effort stderr
			return errExit
		},
	}
}

func newAgentDestroyCmd(_, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Deprecated: use \"gc session close\"",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			fmt.Fprintln(stderr, "gc agent destroy: removed, use \"gc session close\" instead") //nolint:errcheck // best-effort stderr
			return errExit
		},
	}
}
