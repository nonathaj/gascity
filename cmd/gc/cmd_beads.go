package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/gascity/internal/dolt"
)

func newBeadsCmd(stdout, stderr io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "beads",
		Short: "Manage the beads provider",
		Long: `Manage the beads provider (backing store for issue tracking).

Subcommands for health checking and diagnostics.`,
		Args: cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				fmt.Fprintln(stderr, "gc beads: missing subcommand (health)") //nolint:errcheck // best-effort stderr
			} else {
				fmt.Fprintf(stderr, "gc beads: unknown subcommand %q\n", args[0]) //nolint:errcheck // best-effort stderr
			}
			return errExit
		},
	}
	cmd.AddCommand(
		newBeadsHealthCmd(stdout, stderr),
	)
	return cmd
}

func newBeadsHealthCmd(stdout, stderr io.Writer) *cobra.Command {
	var quiet bool
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Check beads provider health",
		Long: `Check beads provider health and attempt recovery on failure.

For the bd (dolt) provider, runs a three-layer health check:
  1. TCP reachability on the configured port
  2. Query probe (SELECT 1)
  3. Write probe (create/write/drop temp table)

If unhealthy, attempts automatic recovery (stop + restart).
For exec providers, delegates to the provider's "health" operation.
For the file provider, always succeeds (no-op).

Also used by the beads-health system automation for periodic monitoring.`,
		Example: `  gc beads health
  gc beads health --quiet`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if doBeadsHealth(quiet, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&quiet, "quiet", false,
		"silent on success, stderr on failure")
	return cmd
}

// doBeadsHealth runs the beads provider health check.
// Returns 0 if healthy, 1 if unhealthy/recovery-failed.
func doBeadsHealth(quiet bool, stdout, stderr io.Writer) int {
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc beads health: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	provider := beadsProvider(cityPath)
	ops := doltHealthOps{
		hostPort: dolt.GasCityConfig(cityPath).HostPort(),
		dialTCP: func(addr string) error {
			conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err == nil {
				_ = conn.Close()
			}
			return err
		},
		queryProbe:     func() error { return dolt.HealthCheckQuery(cityPath) },
		writeProbe:     func() error { return dolt.HealthCheckWrite(cityPath) },
		isUnhealthy:    func() (bool, string) { return dolt.IsUnhealthy(cityPath) },
		setUnhealthy:   func(reason string) { dolt.SetUnhealthy(cityPath, reason) },
		clearUnhealthy: func() { dolt.ClearUnhealthy(cityPath) },
		recover:        func() error { return dolt.RecoverDolt(cityPath) },
	}

	return doBdHealthCheck(provider, os.Getenv("GC_DOLT") == "skip", ops,
		func() error { return healthBeadsProvider(cityPath) },
		quiet, stdout, stderr)
}

// doltHealthOps bundles the external operations needed by the bd health check,
// allowing tests to inject fakes.
type doltHealthOps struct {
	hostPort       string
	dialTCP        func(addr string) error
	queryProbe     func() error
	writeProbe     func() error
	isUnhealthy    func() (bool, string)
	setUnhealthy   func(reason string)
	clearUnhealthy func()
	recover        func() error
}

// doBdHealthCheck runs the beads health check logic with injectable
// dependencies. Extracted from doBeadsHealth for testability.
func doBdHealthCheck(provider string, doltSkip bool, ops doltHealthOps,
	healthFn func() error, quiet bool, stdout, stderr io.Writer,
) int {
	// For non-bd providers, delegate to healthFn directly.
	if provider != "bd" || doltSkip {
		if err := healthFn(); err != nil {
			fmt.Fprintf(stderr, "gc beads health: %v\n", err) //nolint:errcheck // best-effort stderr
			return 1
		}
		if !quiet {
			fmt.Fprintln(stdout, "Beads provider: healthy") //nolint:errcheck // best-effort stdout
		}
		return 0
	}

	// bd provider: run per-tier checks with verbose output.
	allOK := true

	// Tier 1: TCP reachability.
	tcpOK := false
	tcpErr := ops.dialTCP(ops.hostPort)
	if tcpErr == nil {
		tcpOK = true
	}
	if !quiet {
		if tcpOK {
			fmt.Fprintf(stdout, "TCP (%s): ok\n", ops.hostPort) //nolint:errcheck // best-effort stdout
		} else {
			fmt.Fprintf(stdout, "TCP (%s): FAIL (%v)\n", ops.hostPort, tcpErr) //nolint:errcheck // best-effort stdout
		}
	}
	if !tcpOK {
		allOK = false
	}

	// Tier 2: Query probe.
	queryOK := false
	if tcpOK {
		queryErr := ops.queryProbe()
		queryOK = queryErr == nil
		if !quiet {
			if queryOK {
				fmt.Fprintln(stdout, "Query (SELECT 1): ok") //nolint:errcheck // best-effort stdout
			} else {
				fmt.Fprintf(stdout, "Query (SELECT 1): FAIL (%v)\n", queryErr) //nolint:errcheck // best-effort stdout
			}
		}
		if !queryOK {
			allOK = false
		}
	}

	// Tier 3: Write probe.
	if queryOK {
		writeErr := ops.writeProbe()
		writeOK := writeErr == nil
		if !quiet {
			if writeOK {
				fmt.Fprintln(stdout, "Write: ok") //nolint:errcheck // best-effort stdout
			} else {
				fmt.Fprintf(stdout, "Write: FAIL (%v)\n", writeErr) //nolint:errcheck // best-effort stdout
			}
		}
		if !writeOK {
			allOK = false
		}
	}

	// Report DOLT_UNHEALTHY signal status.
	unhealthy, reason := ops.isUnhealthy()
	if !quiet && unhealthy {
		fmt.Fprintf(stdout, "DOLT_UNHEALTHY: %s\n", reason) //nolint:errcheck // best-effort stdout
	}

	if !allOK {
		// Attempt recovery.
		if !quiet {
			fmt.Fprintln(stdout, "Attempting recovery...") //nolint:errcheck // best-effort stdout
		}
		ops.setUnhealthy("health check failed")
		if recErr := ops.recover(); recErr != nil {
			fmt.Fprintf(stderr, "gc beads health: recovery failed: %v\n", recErr) //nolint:errcheck // best-effort stderr
			return 1
		}
		ops.clearUnhealthy()
		if !quiet {
			fmt.Fprintln(stdout, "Recovery successful.") //nolint:errcheck // best-effort stdout
		}
	}

	return 0
}
