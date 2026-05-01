package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"

	"github.com/gastownhall/gascity/internal/beads"
	"github.com/gastownhall/gascity/internal/config"
	"github.com/gastownhall/gascity/internal/events"
	"github.com/gastownhall/gascity/internal/runtime"
	"github.com/spf13/cobra"
)

func newHandoffCmd(stdout, stderr io.Writer) *cobra.Command {
	var target string
	var auto bool
	cmd := &cobra.Command{
		Use:   "handoff [subject] [message]",
		Short: "Send handoff mail and restart this session",
		Long: `Convenience command for context handoff.

Self-handoff (default): sends mail to self and blocks until controller
restarts the session. Equivalent to:

  gc mail send $GC_ALIAS <subject> [message]
  gc runtime request-restart

Auto handoff (--auto): sends mail to self and returns without requesting a
restart. This is for PreCompact hooks, where the provider is already managing
the context compaction lifecycle.

Remote handoff (--target): sends mail to a target session and kills it so the
reconciler restarts it with the handoff mail waiting. Equivalent to:

  gc mail send <target> <subject> [message]
  gc session kill <target>

Self-handoff requires session context (GC_ALIAS or GC_SESSION_ID, plus
GC_SESSION_NAME and city context env). Remote handoff accepts a session alias or ID.`,
		Args: func(cmd *cobra.Command, args []string) error {
			if auto {
				return cobra.MaximumNArgs(2)(cmd, args)
			}
			return cobra.RangeArgs(1, 2)(cmd, args)
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if cmdHandoff(args, target, auto, stdout, stderr) != 0 {
				return errExit
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "Remote session alias or ID to handoff (sends mail + kills session)")
	cmd.Flags().BoolVar(&auto, "auto", false, "Send handoff mail without requesting restart (for PreCompact hooks)")
	return cmd
}

func cmdHandoff(args []string, target string, auto bool, stdout, stderr io.Writer) int {
	if target != "" {
		if auto {
			fmt.Fprintln(stderr, "gc handoff: --auto cannot be used with --target") //nolint:errcheck // best-effort stderr
			return 1
		}
		return cmdHandoffRemote(args, target, stdout, stderr)
	}

	current, err := currentSessionRuntimeTarget()
	if err != nil {
		fmt.Fprintf(stderr, "gc handoff: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	store, err := openCityStoreAt(current.cityPath)
	if err != nil {
		fmt.Fprintf(stderr, "gc handoff: %v\n", err)                    //nolint:errcheck // best-effort stderr
		fmt.Fprintln(stderr, "hint: run \"gc doctor\" for diagnostics") //nolint:errcheck // best-effort stderr
		return 1
	}
	rec := openCityRecorderAt(current.cityPath, stderr)
	if auto {
		return doHandoffAuto(store, rec, current.display, args, stdout, stderr)
	}

	sp := newSessionProvider()
	dops := newDrainOps(sp)
	cfg, _ := loadCityConfig(current.cityPath, stderr)
	persistRestart := sessionRestartPersister(current.cityPath, store, sp, cfg, current.sessionName)

	if code := doHandoff(store, rec, dops, persistRestart, current.display, current.sessionName, args, stdout, stderr); code != 0 {
		return code
	}

	// Block forever. The controller will kill the entire process tree.
	select {}
}

// cmdHandoffRemote sends handoff mail to a remote session and kills its runtime.
// Returns immediately (non-blocking). The reconciler restarts the target.
func cmdHandoffRemote(args []string, target string, stdout, stderr io.Writer) int {
	targetInfo, err := resolveSessionRuntimeTarget(target, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "gc handoff: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}

	store, code := openCityStore(stderr, "gc handoff")
	if store == nil {
		return code
	}
	cityPath, err := resolveCity()
	if err != nil {
		fmt.Fprintf(stderr, "gc handoff: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	cfg, _ := loadCityConfig(cityPath, stderr)
	sender, ok := resolveDefaultMailSenderForCommand(cityPath, cfg, store, stderr, "gc handoff")
	if !ok {
		return 1
	}

	sp := newSessionProvider()
	rec := openCityRecorder(stderr)
	return doHandoffRemote(store, rec, sp, targetInfo.sessionName, targetInfo.display, sender, args, stdout, stderr)
}

func sessionRestartPersister(cityPath string, store beads.Store, sp runtime.Provider, cfg *config.City, target string) func() error {
	if store == nil {
		return nil
	}
	return func() error {
		handle, err := workerHandleForSessionTargetWithConfig(cityPath, store, sp, cfg, target)
		if err != nil {
			return err
		}
		return handle.Reset(context.Background())
	}
}

// doHandoff sends a handoff mail to self and sets the restart-requested flag.
// Testable: does not block.
func doHandoff(store beads.Store, rec events.Recorder, dops drainOps, persistRestart func() error,
	sessionAddress, sessionName string, args []string, stdout, stderr io.Writer,
) int {
	b, ok := createHandoffMail(store, rec, sessionAddress, sessionAddress, args, "HANDOFF: context cycle", stderr)
	if !ok {
		return 1
	}

	if err := dops.setRestartRequested(sessionName); err != nil {
		fmt.Fprintf(stderr, "gc handoff: setting restart flag: %v\n", err) //nolint:errcheck // best-effort stderr
		return 1
	}
	// Also persist the request through the worker boundary so it survives
	// tmux session death. Non-fatal: the runtime flag above is primary.
	if persistRestart != nil {
		if err := persistRestart(); err != nil {
			fmt.Fprintf(stderr, "gc handoff: setting bead restart flag: %v\n", err) //nolint:errcheck // best-effort stderr
		}
	}
	rec.Record(events.Event{
		Type:    events.SessionDraining,
		Actor:   sessionAddress,
		Subject: sessionAddress,
		Message: "handoff",
	})

	fmt.Fprintf(stdout, "Handoff: sent mail %s, requesting restart...\n", b.ID) //nolint:errcheck // best-effort stdout
	return 0
}

// doHandoffAuto sends handoff mail to self without requesting restart.
func doHandoffAuto(store beads.Store, rec events.Recorder, sessionAddress string, args []string, stdout, stderr io.Writer) int {
	b, ok := createHandoffMail(store, rec, sessionAddress, sessionAddress, args, "context cycle", stderr)
	if !ok {
		return 1
	}
	fmt.Fprintf(stdout, "Handoff: sent auto mail %s (restart skipped).\n", b.ID) //nolint:errcheck // best-effort stdout
	return 0
}

func createHandoffMail(store beads.Store, rec events.Recorder, senderAddress, recipientAddress string, args []string, defaultSubject string, stderr io.Writer) (beads.Bead, bool) {
	subject := defaultSubject
	if len(args) > 0 {
		subject = args[0]
	}
	var message string
	if len(args) > 1 {
		message = args[1]
	}
	metadata, err := mailSenderRouteMetadata(store, senderAddress)
	if err != nil {
		fmt.Fprintf(stderr, "gc handoff: resolving sender route: %v\n", err) //nolint:errcheck // best-effort stderr
		return beads.Bead{}, false
	}
	senderDisplay := mailSenderDisplayFromMetadata(senderAddress, metadata)

	b, err := store.Create(beads.Bead{
		Title:       subject,
		Description: message,
		Type:        "message",
		Assignee:    recipientAddress,
		From:        senderDisplay,
		Labels:      []string{"thread:" + handoffThreadID()},
		Metadata:    metadata,
	})
	if err != nil {
		fmt.Fprintf(stderr, "gc handoff: creating mail: %v\n", err) //nolint:errcheck // best-effort stderr
		return beads.Bead{}, false
	}
	rec.Record(events.Event{
		Type:    events.MailSent,
		Actor:   senderDisplay,
		Subject: b.ID,
		Message: recipientAddress,
		Payload: mailEventPayload(nil),
	})
	return b, true
}

// doHandoffRemote sends handoff mail to a remote session and kills its runtime.
// Non-blocking: returns immediately after killing the session.
func doHandoffRemote(store beads.Store, rec events.Recorder, sp runtime.Provider,
	sessionName, targetAddress, sender string, args []string, stdout, stderr io.Writer,
) int {
	b, ok := createHandoffMail(store, rec, sender, targetAddress, args, "HANDOFF: context cycle", stderr)
	if !ok {
		return 1
	}

	// Kill target session (reconciler restarts it).
	running, err := workerSessionTargetRunningWithConfig("", store, sp, nil, sessionName)
	if err != nil {
		fmt.Fprintf(stderr, "gc handoff: observing %s: %v\n", targetAddress, err) //nolint:errcheck // best-effort stderr
		return 1
	}
	if !running {
		fmt.Fprintf(stdout, "Handoff: sent mail %s to %s (session not running; will be delivered on next start)\n", b.ID, targetAddress) //nolint:errcheck // best-effort stdout
		return 0
	}
	if err := workerKillSessionTargetWithConfig("", store, sp, nil, sessionName); err != nil {
		fmt.Fprintf(stderr, "gc handoff: killing %s: %v\n", targetAddress, err) //nolint:errcheck // best-effort stderr
		return 1
	}
	rec.Record(events.Event{
		Type:    events.SessionStopped,
		Actor:   b.From,
		Subject: targetAddress,
		Message: "handoff",
	})

	fmt.Fprintf(stdout, "Handoff: sent mail %s to %s, killed session (reconciler will restart)\n", b.ID, targetAddress) //nolint:errcheck // best-effort stdout
	return 0
}

// handoffThreadID generates a unique thread ID for handoff messages.
func handoffThreadID() string {
	b := make([]byte, 6)
	rand.Read(b) //nolint:errcheck
	return fmt.Sprintf("thread-%x", b)
}
