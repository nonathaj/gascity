//go:build windows

package pidutil

import (
	"net"
	"os"
	"slices"
	"testing"
)

// TestTCPListenerAttribution pins both lookup directions against a
// live in-process listener: port→PID (doctor's managed-dolt state
// validation) and PID→ports (dolt discovery's port attribution).
func TestTCPListenerAttribution(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	pid, checked := TCPListenerPID(port)
	if !checked {
		t.Fatal("TCPListenerPID checked = false on windows")
	}
	if pid != os.Getpid() {
		t.Fatalf("TCPListenerPID(%d) = %d, want own PID %d", port, pid, os.Getpid())
	}

	byPID, checked := TCPListenerPortsByPID()
	if !checked {
		t.Fatal("TCPListenerPortsByPID checked = false on windows")
	}
	if !slices.Contains(byPID[os.Getpid()], port) {
		t.Fatalf("TCPListenerPortsByPID()[%d] = %v, want to contain %d", os.Getpid(), byPID[os.Getpid()], port)
	}

	if pid, _ := TCPListenerPID(0); pid != 0 {
		t.Fatalf("TCPListenerPID(0) = %d, want 0", pid)
	}
}
