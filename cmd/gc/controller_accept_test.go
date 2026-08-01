package main

import (
	"errors"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// scriptedListener returns a canned sequence of Accept results.
type scriptedListener struct {
	mu    sync.Mutex
	steps []func() (net.Conn, error)
	next  int
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.next >= len(l.steps) {
		return nil, net.ErrClosed
	}
	step := l.steps[l.next]
	l.next++
	return step()
}

func (l *scriptedListener) Close() error   { return nil }
func (l *scriptedListener) Addr() net.Addr { return &net.UnixAddr{Name: "scripted", Net: "unix"} }

// TestAcceptControllerConnsSurvivesTransientAcceptError is the regression for
// gw-sph. A single non-close Accept error must not end the loop: the accept
// goroutine is the controller's only ear, and giving up leaves the process
// alive but permanently deaf. A client then connects successfully — the socket
// still exists — and blocks for its entire read deadline waiting for an
// acknowledgement that can never come, which is why raising the deadline does
// not help and why the observed hang was unbounded rather than merely slow.
func TestAcceptControllerConnsSurvivesTransientAcceptError(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})

	lis := &scriptedListener{steps: []func() (net.Conn, error){
		func() (net.Conn, error) { return nil, errors.New("transient accept failure") },
		func() (net.Conn, error) { return server, nil },
	}}

	served := make(chan net.Conn, 1)
	done := make(chan struct{})
	go func() {
		acceptControllerConns(lis, func(c net.Conn) { served <- c })
		close(done)
	}()

	select {
	case got := <-served:
		if got != server {
			t.Fatalf("served unexpected connection %v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("connection after a transient Accept error was never served: the accept loop gave up and the controller is deaf")
	}

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("accept loop did not return after the listener reported ErrClosed")
	}
}

// TestAcceptControllerConnsStopsWhenListenerCloses pins the other half: a
// closed listener must end the loop rather than spin on it.
func TestAcceptControllerConnsStopsWhenListenerCloses(t *testing.T) {
	lis := &scriptedListener{}
	done := make(chan struct{})
	go func() {
		acceptControllerConns(lis, func(net.Conn) { t.Error("served a connection from a closed listener") })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("accept loop did not return on a closed listener")
	}
}

// TestCloseControllerConnDeliversReplyToASlowReader is the regression for the
// second half of gw-sph. The controller writes its acknowledgement and returns
// immediately; on Windows, closing an AF_UNIX connection discards data the peer
// has not read yet, so a client that has not been scheduled in between loses
// the reply entirely. The server sees a successful write and the client waits
// out its whole deadline, which is why the failure looked like a slow
// acknowledgement rather than a dropped one.
//
// The delay before reading is the point of the test: it stands in for the
// scheduling gap that load produces.
func TestCloseControllerConnDeliversReplyToASlowReader(t *testing.T) {
	dir := shortSocketTempDir(t, "gc-ackclose-")
	sock := filepath.Join(dir, "s.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() })

	served := make(chan struct{})
	go func() {
		conn, aerr := lis.Accept()
		if aerr != nil {
			close(served)
			return
		}
		_, _ = conn.Write([]byte("ok\n"))
		closeControllerConn(conn)
		close(served)
	}()

	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// Do not read yet: let the server write and close first.
	time.Sleep(300 * time.Millisecond)

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	got, err := io.ReadAll(io.LimitReader(conn, 64))
	if err != nil {
		t.Fatalf("reading acknowledgement after the server closed: %v", err)
	}
	if string(got) != "ok\n" {
		t.Fatalf("acknowledgement = %q, want %q (the reply was discarded by the close)", string(got), "ok\n")
	}

	select {
	case <-served:
	case <-time.After(10 * time.Second):
		t.Fatal("server side did not finish")
	}
}
