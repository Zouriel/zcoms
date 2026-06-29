package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Client talks to the core comms daemon's IPC socket. It is the one seam every
// upper tier uses to reach Telegram/WhatsApp and the contacts directory.
type Client struct {
	socket string
	caller string // "owner" | "agent"; tags store writes. Empty = owner.
}

// New returns a client for the given socket path. Use DefaultSocketPath for the
// standard location.
func New(socket string) *Client { return &Client{socket: socket} }

// NewDefault returns a client pointed at ~/.config/zcoms/daemon.sock.
func NewDefault() (*Client, error) {
	p, err := DefaultSocketPath()
	if err != nil {
		return nil, err
	}
	return &Client{socket: p}, nil
}

// AsCaller returns a copy of the client that tags its store writes with the
// given caller identity ("owner" | "agent"). The running agent uses
// AsCaller("agent"); the CLI/console are owner.
func (c *Client) AsCaller(caller string) *Client {
	cp := *c
	cp.caller = caller
	return &cp
}

// Available reports whether the daemon is listening.
func (c *Client) Available() bool {
	conn, err := net.DialTimeout("unix", c.socket, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Hello fetches the daemon's advertised ProtocolVersion. Upper tiers call this
// on start and exit loudly if it disagrees with their compiled ProtocolVersion.
func (c *Client) Hello() (int, error) {
	resp, err := c.Do(Request{Op: "hello"}, time.Now().Add(5*time.Second))
	return resp.Version, err
}

// CheckProtocol dials the daemon and verifies its version matches ours, so a
// stale module fails fast with a clear message instead of misbehaving.
func (c *Client) CheckProtocol() error {
	v, err := c.Hello()
	if err != nil {
		return err
	}
	if v != ProtocolVersion {
		return fmt.Errorf("comms protocol mismatch: daemon speaks v%d, this build speaks v%d — update to match", v, ProtocolVersion)
	}
	return nil
}

// Do sends one request and reads one response. readDeadline bounds the wait for
// the response (zero = no deadline, for blocking ops like ask). The client's
// ProtocolVersion and caller identity are stamped onto every request.
func (c *Client) Do(req Request, readDeadline time.Time) (Response, error) {
	if req.Version == 0 {
		req.Version = ProtocolVersion
	}
	if req.Caller == "" && c.caller != "" {
		req.Caller = c.caller
	}
	conn, err := net.DialTimeout("unix", c.socket, 2*time.Second)
	if err != nil {
		return Response{}, err
	}
	defer conn.Close()

	line, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return Response{}, err
	}
	if !readDeadline.IsZero() {
		_ = conn.SetReadDeadline(readDeadline)
	}
	respLine, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(respLine) == 0 {
		return Response{}, err
	}
	var resp Response
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return Response{}, err
	}
	if !resp.OK {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
}

// Address identifies a conversation on a specific transport (mirrors
// transport.Address). ID is the transport-native id: a Telegram @username or
// chat id, a WhatsApp JID, an Instagram thread/user id.
type Address struct {
	Transport string `json:"transport"`
	ID        string `json:"id"`
}

// Send delivers a one-way message on Telegram; To is an @username or numeric
// chat id. (Kept for the many existing callers; SendOn targets any transport.)
func (c *Client) Send(to, text string) (Response, error) {
	return c.Do(Request{Op: "send", To: to, Text: text}, time.Now().Add(30*time.Second))
}

// SendOn delivers a one-way message on the named transport ("telegram" |
// "whatsapp" | "instagram"); to is that transport's native id. The reply path
// for inbound messages uses this so an answer returns on the same app.
func (c *Client) SendOn(transport, to, text string) (Response, error) {
	return c.Do(Request{Op: "send", Transport: transport, To: to, Text: text}, time.Now().Add(30*time.Second))
}

// SendAddr is SendOn with an Address value.
func (c *Client) SendAddr(addr Address, text string) (Response, error) {
	return c.SendOn(addr.Transport, addr.ID, text)
}

// SendFileOn uploads a local file on the named transport.
func (c *Client) SendFileOn(transport, to, path, caption string) (Response, error) {
	return c.Do(Request{Op: "sendfile", Transport: transport, To: to, Path: path, Text: caption}, time.Now().Add(31*time.Minute))
}

// Connectors returns the live status of every transport the daemon knows
// (Telegram/WhatsApp/Instagram and any reserved slots) for the connectors page.
func (c *Client) Connectors() ([]Connector, error) {
	resp, err := c.Do(Request{Op: "connectors"}, time.Now().Add(10*time.Second))
	return resp.Connectors, err
}

// ConnectorAction runs a connect/disconnect action on a transport from the
// connectors page — e.g. ("whatsapp", "reconnect") to re-arm a fresh QR after
// one expired, or ("whatsapp", "logout") to sign out.
func (c *Client) ConnectorAction(transport, action string) error {
	_, err := c.Do(Request{Op: "connector_action", Transport: transport, Text: action}, time.Now().Add(20*time.Second))
	return err
}

// Ask sends a question and blocks until the user replies (no deadline).
func (c *Client) Ask(to, text string) (string, error) {
	resp, err := c.Do(Request{Op: "ask", To: to, Text: text}, time.Time{})
	return resp.Reply, err
}

// SendFile uploads a local file (waits for the upload).
func (c *Client) SendFile(to, path, caption string) (Response, error) {
	return c.Do(Request{Op: "sendfile", To: to, Path: path, Text: caption}, time.Now().Add(31*time.Minute))
}

// Read fetches the last count history messages of a chat.
func (c *Client) Read(to string, count int, download bool) (Response, error) {
	d := time.Now().Add(60 * time.Second)
	if download {
		d = time.Now().Add(5 * time.Minute)
	}
	return c.Do(Request{Op: "read", To: to, Count: count, Download: download}, d)
}

// ReadOn fetches a chat's recent history on a specific transport (to is that
// transport's native id, e.g. a WhatsApp JID). Telegram callers use Read.
func (c *Client) ReadOn(transport, to string, count int) (Response, error) {
	return c.Do(Request{Op: "read", Transport: transport, To: to, Count: count}, time.Now().Add(60*time.Second))
}

// Unread returns unread 1:1 messages across every transport the daemon serves
// (Telegram plus any transport that keeps a readable store, e.g. WhatsApp). Each
// item carries its Transport so callers can route a reply / mark-read correctly.
func (c *Client) Unread() ([]UnreadItem, error) {
	resp, err := c.Do(Request{Op: "unread"}, time.Now().Add(60*time.Second))
	return resp.Unread, err
}

// MarkRead marks the given Telegram messages in a chat as read.
func (c *Client) MarkRead(chatID int64, messageIDs []int64) error {
	_, err := c.Do(Request{Op: "mark_read", ChatID: chatID, MessageIDs: messageIDs}, time.Now().Add(30*time.Second))
	return err
}

// MarkReadOn marks messages read on a non-Telegram transport (address is the
// native chat id, e.g. a WhatsApp JID; refs are the string message ids).
func (c *Client) MarkReadOn(transport, address string, refs []string) error {
	_, err := c.Do(Request{Op: "mark_read", Transport: transport, To: address, MsgRefs: refs}, time.Now().Add(30*time.Second))
	return err
}

// Resolve maps an @username (or numeric id) to a Telegram chat id.
func (c *Client) Resolve(to string) (int64, error) {
	resp, err := c.Do(Request{Op: "resolve", To: to}, time.Now().Add(30*time.Second))
	return resp.ChatID, err
}
