package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
)

// Subscribe opens a streaming subscription for role (a module/role name like
// "bridge" or "errands") and calls handler for each incoming message event until
// the connection drops (then it returns the error). Callers typically run this
// in a reconnect loop — see Harness.Run.
//
// Subscribe blocks on the daemon's connection and cannot be interrupted: a
// module that must shut down promptly should use SubscribeContext instead.
func (c *Client) Subscribe(role string, handler func(Event)) error {
	return c.SubscribeContext(context.Background(), role, handler)
}

// SubscribeContext is Subscribe with a cancellable read. When ctx is cancelled it
// closes the connection, which unblocks the read immediately and returns
// ctx.Err() — without this, a subscriber sits in a blocking read on a socket the
// daemon has no reason to close, so a service holding one takes its full
// systemd stop timeout and dies on SIGKILL rather than exiting on SIGTERM.
func (c *Client) SubscribeContext(ctx context.Context, role string, handler func(Event)) error {
	conn, err := net.Dial("unix", c.socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	line, _ := json.Marshal(Request{Op: "subscribe", Role: role, Version: ProtocolVersion})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return err
	}

	// Closing the conn is what unblocks sc.Scan; the goroutine exits either way
	// via the done channel, so it can't outlive this call.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Event == "message" {
			handler(ev)
		}
	}
	// A cancelled context closed the connection under the scanner, so report that
	// rather than the resulting "use of closed network connection".
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("subscription closed by daemon")
}
