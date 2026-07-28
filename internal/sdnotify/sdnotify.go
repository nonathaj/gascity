// Package sdnotify implements the client side of the systemd
// sd_notify(3) readiness protocol: short state strings sent as
// datagrams over the unix socket named by the NOTIFY_SOCKET
// environment variable. The protocol is a single unixgram write, so
// the package is deliberately dependency-free.
package sdnotify

import (
	"net"
	"os"
	"time"
)

// State strings from the sd_notify(3) protocol understood by systemd.
const (
	// Ready tells the service manager that startup is finished
	// (Type=notify units stay "activating" until this arrives).
	Ready = "READY=1"
	// Watchdog updates the service watchdog timestamp (WatchdogSec=).
	Watchdog = "WATCHDOG=1"
	// Reloading tells the service manager that a configuration reload
	// has begun; per sd_notify(3) the reload ends with a Ready send.
	Reloading = "RELOADING=1"
	// Stopping tells the service manager that shutdown has begun.
	Stopping = "STOPPING=1"
)

// writeTimeout bounds the datagram send so a wedged notify socket can
// never stall the caller (systemd drains the socket promptly; anything
// else holding it is broken).
const writeTimeout = time.Second

// Notify sends state over the unix datagram socket named by the
// NOTIFY_SOCKET environment variable, following sd_notify(3). When
// NOTIFY_SOCKET is unset or empty — the process is not running under a
// notify-aware service manager — it is a no-op returning (false, nil).
// It returns (true, nil) once the datagram is written, and
// (false, err) when NOTIFY_SOCKET is set but the send fails.
func Notify(state string) (bool, error) {
	// GC_NOTIFY_PIPE is the Windows transport (gw-x1k, D4 of
	// engdocs/design/windows-systemd-parity.md). sd_notify is a unixgram protocol, so
	// without it Notify is a permanent no-op on Windows and the four states gc already
	// sends at ready/reload/stopping/watchdog points are discarded — nothing hosting the
	// supervisor there can observe that it came up.
	//
	// Checked before NOTIFY_SOCKET only so a host that sets both gets the transport that
	// can actually work on its platform; in practice exactly one is ever set.
	if pipe := os.Getenv("GC_NOTIFY_PIPE"); pipe != "" {
		return notifyPipe(pipe, state)
	}
	socket := os.Getenv("NOTIFY_SOCKET")
	if socket == "" {
		return false, nil
	}
	// A leading '@' names a Linux abstract-namespace socket; the wire
	// form replaces it with a NUL byte.
	if socket[0] == '@' {
		socket = "\x00" + socket[1:]
	}
	conn, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return false, err
	}
	defer conn.Close() //nolint:errcheck
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return false, err
	}
	if _, err := conn.Write([]byte(state)); err != nil {
		return false, err
	}
	return true, nil
}

// notifyPipe writes state to the named pipe at path, the Windows transport for the same
// sd_notify(3) state strings.
//
// The wire form is deliberately identical to the unix path — a bare "READY=1" and friends,
// one write per call — so a reader can be shared between the two and the existing call sites
// need no platform awareness.
//
// No named-pipe library is needed for the client half: opening the pipe path with os.OpenFile
// connects to an existing server, which keeps this package dependency-free as its doc comment
// promises. Creating a pipe SERVER does require platform calls, but only a host or a test does
// that, never Notify.
//
// A configured-but-dead pipe is an error rather than a silent success, matching
// NOTIFY_SOCKET's behavior: whoever set the variable is waiting for readiness, and reporting
// "sent" for a message nobody received would leave them waiting on a state that never arrives.
func notifyPipe(path, state string) (bool, error) {
	conn, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return false, err
	}
	defer conn.Close() //nolint:errcheck
	if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		// Not every pipe handle supports deadlines; the bounded write below still applies
		// because a named-pipe write to a live reader does not block indefinitely.
		_ = err
	}
	if _, err := conn.Write([]byte(state)); err != nil {
		return false, err
	}
	return true, nil
}
