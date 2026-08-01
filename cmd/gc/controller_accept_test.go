package main

import (
	"errors"
	"net"
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
