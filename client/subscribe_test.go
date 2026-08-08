package client

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// idleDaemon accepts one subscriber and then says nothing, like a real daemon
// with no traffic. It never closes the connection, which is exactly the state a
// blocking subscriber gets stuck in.
func idleDaemon(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	conns := make(chan net.Conn, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			conns <- c
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		close(conns)
		for c := range conns {
			c.Close()
		}
	})
	return sock
}

// A cancelled context must unblock the read. Without this the caller sits in
// sc.Scan() until the daemon closes the connection, so a module holding a
// subscription waits out its whole systemd stop timeout and is SIGKILLed.
func TestSubscribeContextCancelUnblocks(t *testing.T) {
	c := New(idleDaemon(t))
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- c.SubscribeContext(ctx, "test", func(Event) {}) }()

	// Let the subscription reach its blocking read before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SubscribeContext did not return after its context was cancelled")
	}
}

// A context cancelled before the call returns promptly too, rather than opening a
// subscription nothing will ever read.
func TestSubscribeContextAlreadyCancelled(t *testing.T) {
	c := New(idleDaemon(t))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- c.SubscribeContext(ctx, "test", func(Event) {}) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SubscribeContext blocked on an already-cancelled context")
	}
}

// The daemon closing the connection is still reported as such, not as a
// cancellation, so the harness logs the real reason and reconnects.
func TestSubscribeContextDaemonClose(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Drop the subscriber as soon as it has subscribed.
		time.Sleep(20 * time.Millisecond)
		conn.Close()
	}()

	done := make(chan error, 1)
	go func() { done <- New(sock).SubscribeContext(context.Background(), "test", func(Event) {}) }()

	select {
	case err := <-done:
		if err == nil || errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want the daemon-closed error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SubscribeContext did not notice the daemon closing")
	}
}
