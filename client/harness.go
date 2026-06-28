package client

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"
)

// Harness is the generic component runtime, consolidated from what every
// component (bridge, errands, …) used to hand-roll in its own comp.go/main.go:
// dialing the daemon, running a subscribe stream in a reconnect loop, and
// serving its own command socket. A component supplies its domain handlers; the
// harness owns the plumbing and lifecycle.
type Harness struct {
	Client *Client
	Log    *log.Logger
}

// NewHarness returns a harness wired to the default daemon socket.
func NewHarness(name string) (*Harness, error) {
	c, err := NewDefault()
	if err != nil {
		return nil, err
	}
	return &Harness{Client: c, Log: log.New(os.Stderr, "["+name+"] ", log.LstdFlags)}, nil
}

// Run subscribes to the daemon's event stream for role and dispatches each event
// to handler, reconnecting with backoff until ctx is cancelled. This is the loop
// every push-driven component (bridge, errands) used to copy by hand.
func (h *Harness) Run(ctx context.Context, role string, handler func(Event)) {
	for ctx.Err() == nil {
		err := h.Client.Subscribe(role, handler)
		if ctx.Err() != nil {
			return
		}
		h.Log.Printf("subscription %q ended (%v); reconnecting…", role, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// CommandRequest is one line on a component's own command socket
// (errands.sock / team.sock / …): a text command and who issued it.
type CommandRequest struct {
	Text  string `json:"text"`
	Actor string `json:"actor,omitempty"`
}

// CommandResponse is the component's reply on its command socket. Continue keeps
// a multi-turn flow (e.g. team add-task) open for the next line.
type CommandResponse struct {
	OK       bool   `json:"ok"`
	Reply    string `json:"reply,omitempty"`
	Continue bool   `json:"continue,omitempty"`
	Error    string `json:"error,omitempty"`
}

// ServeCommands listens on <socketFile> in the app dir (e.g. "errands.sock") and
// invokes handler for each command line, replying with one JSON response. It
// blocks until ctx is cancelled, then removes the socket. This is the server
// half of ComponentCommand / the daemon's module router.
func (h *Harness) ServeCommands(ctx context.Context, socketFile string, handler func(CommandRequest) CommandResponse) error {
	dir, err := DefaultAppDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, socketFile)
	_ = os.Remove(path) // clear a stale socket from an unclean exit
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = os.Remove(path)
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go h.serveCommandConn(conn, handler)
	}
}

func (h *Harness) serveCommandConn(conn net.Conn, handler func(CommandRequest) CommandResponse) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}
	var req CommandRequest
	if json.Unmarshal(line, &req) != nil {
		writeCommand(conn, CommandResponse{Error: "bad request"})
		return
	}
	writeCommand(conn, handler(req))
}

func writeCommand(conn net.Conn, resp CommandResponse) {
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
}
