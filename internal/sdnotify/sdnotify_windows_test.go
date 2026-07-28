//go:build windows

package sdnotify

import (
	"fmt"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// startNotifyPipeServer creates a byte-mode named pipe and returns its name plus a channel
// delivering the first message written to it.
//
// A server is needed because Go's standard library can only DIAL a named pipe, not create
// one. golang.org/x/sys/windows is used rather than adding github.com/Microsoft/go-winio:
// x/sys/windows is already a direct dependency here (internal/winjob, internal/fsys,
// internal/execgrace all use it), and the sdnotify package documents itself as deliberately
// dependency-free — a property worth keeping for a package whose whole job is one write.
func startNotifyPipeServer(t *testing.T) (string, <-chan string) {
	t.Helper()
	name := fmt.Sprintf(`\\.\pipe\gc-sdnotify-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatalf("pipe name: %v", err)
	}
	handle, err := windows.CreateNamedPipe(
		namep,
		windows.PIPE_ACCESS_INBOUND,
		windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
		1,    // one instance is enough: Notify opens, writes, closes
		0,    // out buffer (inbound only)
		4096, // in buffer
		0,    // default timeout
		nil,
	)
	if err != nil {
		t.Fatalf("CreateNamedPipe(%s): %v", name, err)
	}
	t.Cleanup(func() {
		// Release the server by CONNECTING to it, not by cancelling.
		//
		// The goroutine below blocks in ConnectNamedPipe until a client arrives. When a test
		// fails before Notify connects — which is exactly what happens while this transport
		// is unimplemented — CloseHandle blocks behind that pending I/O and the package
		// burns its full 10m timeout instead of reporting the real failure. CancelIoEx does
		// not fix it either: ConnectNamedPipe with a nil OVERLAPPED is SYNCHRONOUS, and
		// cancellation of synchronous I/O is not reliable. A one-byte-free client connect
		// completes the pending connect deterministically, after which ReadFile sees EOF and
		// the goroutine returns.
		if f, err := os.OpenFile(name, os.O_WRONLY, 0); err == nil {
			_ = f.Close()
		}
		_ = windows.CloseHandle(handle)
	})

	received := make(chan string, 1)
	go func() {
		// ConnectNamedPipe returns ERROR_PIPE_CONNECTED when the client won the race and
		// connected before this call — that is success, not failure.
		if err := windows.ConnectNamedPipe(handle, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
			return
		}
		buf := make([]byte, 4096)
		var n uint32
		if err := windows.ReadFile(handle, buf, &n, nil); err != nil {
			return
		}
		received <- string(buf[:n])
	}()
	return name, received
}

// TestNotifySendsStateOverNamedPipe is the Windows readiness transport contract (gw-x1k, D4).
//
// sd_notify is a unixgram protocol, so on Windows Notify has always been a no-op: the four
// capabilities gc already calls at ready/reload/stopping/watchdog points
// (notifySdState) were dead code there, and this package's tests skipped the platform
// entirely. Anything hosting the supervisor on Windows — the Scheduled Task wrapper, a test
// harness, `gc start` waiting for readiness — has had no way to observe that the supervisor
// came up.
//
// GC_NOTIFY_PIPE names a Windows named pipe carrying the SAME state strings, so every
// existing call site works unchanged and the states become observable rather than discarded.
func TestNotifySendsStateOverNamedPipe(t *testing.T) {
	// All four states, not just READY. The point of this transport is that the capabilities
	// gc already calls — notifySdState at ready, reload, stopping and watchdog points — stop
	// being discarded on Windows. Covering only READY would leave three of them unproven
	// while the bead claimed all four were testable.
	for _, state := range []string{Ready, Reloading, Stopping, Watchdog} {
		t.Run(state, func(t *testing.T) {
			name, received := startNotifyPipeServer(t)
			t.Setenv("NOTIFY_SOCKET", "")
			t.Setenv("GC_NOTIFY_PIPE", name)

			sent, err := Notify(state)
			if err != nil {
				t.Fatalf("Notify(%s) error = %v, want nil", state, err)
			}
			if !sent {
				t.Fatalf("Notify(%s) sent = false, want true when GC_NOTIFY_PIPE names a live pipe", state)
			}
			select {
			case got := <-received:
				if got != state {
					t.Fatalf("pipe received %q, want %q — the wire form must match sd_notify(3) "+
						"so a reader can be shared with the unix transport", got, state)
				}
			case <-time.After(10 * time.Second):
				t.Fatalf("no state arrived on the notify pipe for %s", state)
			}
		})
	}
}

// TestNotifyPipeNoopWhenUnset pins that the pipe transport does not change the contract for
// a process running under no service manager at all: both variables unset means no-op, not
// an error, exactly as the unix path behaves.
func TestNotifyPipeNoopWhenUnset(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	t.Setenv("GC_NOTIFY_PIPE", "")

	sent, err := Notify(Ready)
	if err != nil {
		t.Fatalf("Notify() error = %v, want nil when neither transport is configured", err)
	}
	if sent {
		t.Fatal("Notify() sent = true, want false when neither NOTIFY_SOCKET nor GC_NOTIFY_PIPE is set")
	}
}

// TestNotifyPipeErrorWhenUnreachable pins that a CONFIGURED but dead pipe is an error rather
// than a silent success. A host that set GC_NOTIFY_PIPE is waiting for readiness; silently
// reporting "sent" would leave it waiting forever with nothing to show for it.
func TestNotifyPipeErrorWhenUnreachable(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	t.Setenv("GC_NOTIFY_PIPE", fmt.Sprintf(`\\.\pipe\gc-sdnotify-missing-%d`, time.Now().UnixNano()))

	sent, err := Notify(Ready)
	if err == nil {
		t.Fatal("Notify() error = nil for a pipe that does not exist, want an error")
	}
	if sent {
		t.Fatal("Notify() sent = true for a pipe that does not exist, want false")
	}
}
