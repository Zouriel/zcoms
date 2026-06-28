// Package whatsapp is a thin client over the Node Baileys sidecar's Unix-domain
// socket. It mirrors the tdlib calls triage already uses (fetch unread, mark
// read, send) so triage can treat WhatsApp the same way it treats Telegram.
//
// Wire protocol: newline-delimited JSON, one request line -> one response line.
//
//	request:  {"op":"unread"}
//	          {"op":"read","chatId":"...","msgIds":["..."]}
//	          {"op":"send","chatId":"...","text":"..."}
//	response: {"ok":true,"unread":[{chatId,sender,text,ts,msgId}]}
//	          {"ok":true}
//	          {"ok":false,"error":"..."}
package whatsapp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// dialTimeout bounds connecting to the sidecar; ioTimeout bounds a request so a
// wedged sidecar can't stall a triage pass.
const (
	dialTimeout = 3 * time.Second
	ioTimeout   = 15 * time.Second
)

// Unread is one unread WhatsApp message from a 1:1 chat.
type Unread struct {
	ChatID string
	Sender string
	Text   string
	MsgID  string
	When   time.Time
	File   string // local path to a downloaded attachment, "" for text-only
}

// wire request/response shapes (kept separate from the exported types so the
// JSON tags and the unix-seconds timestamp don't leak into callers).
type request struct {
	Op     string   `json:"op"`
	ChatID string   `json:"chatId,omitempty"`
	MsgIDs []string `json:"msgIds,omitempty"`
	Text   string   `json:"text,omitempty"`
	Path   string   `json:"path,omitempty"` // local file path for sendfile
}

type wireUnread struct {
	ChatID string `json:"chatId"`
	Sender string `json:"sender"`
	Text   string `json:"text"`
	TS     int64  `json:"ts"` // unix seconds
	MsgID  string `json:"msgId"`
	File   string `json:"file"` // local path to downloaded media, "" if none
}

type response struct {
	OK     bool         `json:"ok"`
	Error  string       `json:"error"`
	Unread []wireUnread `json:"unread"`
	Ready  bool         `json:"ready"`
	Chats  int          `json:"chats"`
	QR     string       `json:"qr"`
}

// Status reports whether the sidecar is reachable and paired/connected.
type Status struct {
	Ready bool // WhatsApp connection is open (paired and online)
	Chats int  // number of 1:1 chats the sidecar is mirroring
}

// roundtrip sends one request and decodes the single-line response. Any socket
// or protocol problem is wrapped so callers can log-and-skip without crashing.
func roundtrip(sock string, req request) (response, error) {
	conn, err := net.DialTimeout("unix", sock, dialTimeout)
	if err != nil {
		return response{}, fmt.Errorf("whatsapp: dial %s: %w", sock, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))

	line, err := json.Marshal(req)
	if err != nil {
		return response{}, fmt.Errorf("whatsapp: encode request: %w", err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return response{}, fmt.Errorf("whatsapp: write request: %w", err)
	}

	// Responses can exceed bufio's default line cap when many chats are unread.
	r := bufio.NewReaderSize(conn, 1<<20)
	respLine, err := r.ReadBytes('\n')
	if err != nil && len(respLine) == 0 {
		return response{}, fmt.Errorf("whatsapp: read response: %w", err)
	}
	var resp response
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return response{}, fmt.Errorf("whatsapp: decode response: %w", err)
	}
	if !resp.OK {
		msg := resp.Error
		if msg == "" {
			msg = "unknown error"
		}
		return resp, fmt.Errorf("whatsapp: %s", msg)
	}
	return resp, nil
}

// FetchUnread returns unread 1:1 messages from the sidecar (groups, broadcasts
// and status updates are filtered out on the Node side).
func FetchUnread(sock string) ([]Unread, error) {
	resp, err := roundtrip(sock, request{Op: "unread"})
	if err != nil {
		return nil, err
	}
	out := make([]Unread, 0, len(resp.Unread))
	for _, u := range resp.Unread {
		out = append(out, Unread{
			ChatID: u.ChatID,
			Sender: u.Sender,
			Text:   u.Text,
			MsgID:  u.MsgID,
			When:   time.Unix(u.TS, 0),
			File:   u.File,
		})
	}
	return out, nil
}

// MarkRead marks the given messages read in a WhatsApp chat. This sends
// WhatsApp read receipts (blue ticks) to the sender.
func MarkRead(sock, chatID string, msgIDs []string) error {
	if len(msgIDs) == 0 {
		return nil
	}
	_, err := roundtrip(sock, request{Op: "read", ChatID: chatID, MsgIDs: msgIDs})
	return err
}

// Dismiss clears the given messages from the sidecar's unread mirror without
// touching WhatsApp, so they aren't re-triaged but no read receipt is sent.
// Used for read-only triage so reading a personal inbox leaves no blue ticks.
func Dismiss(sock, chatID string, msgIDs []string) error {
	if len(msgIDs) == 0 {
		return nil
	}
	_, err := roundtrip(sock, request{Op: "dismiss", ChatID: chatID, MsgIDs: msgIDs})
	return err
}

// SendFile uploads a local file (photo/video/audio/document, chosen by
// extension) to a WhatsApp chat, with an optional caption.
func SendFile(sock, chatID, path, caption string) error {
	_, err := roundtrip(sock, request{Op: "sendfile", ChatID: chatID, Path: path, Text: caption})
	return err
}

// Send delivers a text message to a WhatsApp chat.
func Send(sock, chatID, text string) error {
	_, err := roundtrip(sock, request{Op: "send", ChatID: chatID, Text: text})
	return err
}

// Ping reports the sidecar's connection status. It succeeds even before the
// WhatsApp session is paired (Ready=false), so it can tell "sidecar down" from
// "sidecar up but not yet paired".
func Ping(sock string) (Status, error) {
	resp, err := roundtrip(sock, request{Op: "status"})
	if err != nil {
		return Status{}, err
	}
	return Status{Ready: resp.Ready, Chats: resp.Chats}, nil
}

// QR returns the current pairing QR (ASCII-rendered) when the sidecar is up but
// not yet paired. ready is true once linked, in which case qr is empty.
func QR(sock string) (ready bool, qr string, err error) {
	resp, err := roundtrip(sock, request{Op: "qr"})
	if err != nil {
		return false, "", err
	}
	return resp.Ready, resp.QR, nil
}
