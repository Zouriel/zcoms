// Package client is the published contract for the zcoms comms foundation. The
// core daemon (which owns the single Telegram session and the WhatsApp sidecar)
// serves it over a Unix socket speaking newline-delimited JSON: one Request line
// in, one Response line out (the `subscribe` op streams Events).
//
// Every tier above comms (the agent layer, modules) imports this package to
// reach the daemon — they never open another tier's database or socket directly.
// The wire types here are the invariant: their JSON shape must stay compatible
// across versions; breaking changes bump ProtocolVersion (see version.go).
package client

import "path/filepath"

const socketName = "daemon.sock"

// DefaultSocketPath returns ~/.config/zcoms/daemon.sock (the core daemon's IPC
// socket). Callers dial this to reuse the daemon's Telegram session.
func DefaultSocketPath() (string, error) {
	dir, err := DefaultAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, socketName), nil
}

// Request is one command sent to the daemon. Op selects which fields are used.
type Request struct {
	Op       string `json:"op"`                 // send|sendfile|read|ask|unread|mark_read|resolve|hello|contact_*|errand_*
	To       string `json:"to,omitempty"`       // @username or numeric chat id / errand target
	Text     string `json:"text,omitempty"`     // message body / question / caption
	Path     string `json:"path,omitempty"`     // local file path (sendfile)
	Count    int    `json:"count,omitempty"`    // history messages (read)
	Download bool   `json:"download,omitempty"` // download media in a read

	// Protocol handshake: the client advertises the version it speaks. The
	// daemon rejects a mismatch (see version.go). Zero = legacy/unset; the
	// daemon treats absent as "no claim" so older callers keep working.
	Version int `json:"version,omitempty"`

	// Caller identity for store writes: "owner" (trusted: the CLI / console) or
	// "agent" (the running agent — untrusted for crown-jewel tables). Absent =
	// owner for backward-compatible local CLI calls.
	Caller string `json:"caller,omitempty"`

	// mark_read
	ChatID     int64   `json:"chat_id,omitempty"`
	MessageIDs []int64 `json:"message_ids,omitempty"`

	// subscribe: which event stream to receive (a module/role name).
	Role string `json:"role,omitempty"`

	// Contacts store ops (comms.db). Contact carries the row for create/update;
	// Platform/Handle address a single handle.
	Contact  *Contact `json:"contact,omitempty"`
	Platform string   `json:"platform,omitempty"`
	Handle   string   `json:"handle,omitempty"`

	// Errand ops.
	Brief     string `json:"brief,omitempty"`
	Deliver   bool   `json:"deliver,omitempty"`
	AutoStart bool   `json:"auto_start,omitempty"`
	ID        string `json:"id,omitempty"`
}

// Message is one history message returned by the "read" op (mirrors the fields
// `zc tg chat` prints).
type Message struct {
	MessageID int64  `json:"message_id"`
	ChatID    int64  `json:"chat_id"`
	Date      int64  `json:"date"`
	Outgoing  bool   `json:"outgoing"`
	Sender    string `json:"sender"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	File      string `json:"file,omitempty"`
}

// UnreadItem is one unread 1:1 message from a non-allow-listed sender, returned
// by the "unread" op (Telegram only — components merge WhatsApp via the sidecar).
type UnreadItem struct {
	Sender string `json:"sender"`
	Text   string `json:"text"`
	When   int64  `json:"when"` // unix seconds
	ChatID int64  `json:"chat_id"`
	MsgID  int64  `json:"msg_id"`
}

// Event is one pushed message on a subscribe stream: an incoming 1:1 message the
// daemon routed to this subscriber (by role/module name).
type Event struct {
	Event     string `json:"event"` // "message"
	ChatID    int64  `json:"chat_id"`
	UserID    int64  `json:"user_id"`
	Sender    string `json:"sender"`
	Text      string `json:"text"`
	Kind      string `json:"kind"`           // tdlib content type, e.g. "messageText"
	File      string `json:"file,omitempty"` // local path if media was downloaded
	MessageID int64  `json:"message_id"`
	Date      int64  `json:"date"`
}

// Contact is a person in the comms-owned contacts directory (comms.db), with
// their per-platform handles. It is addressing data — every tier resolves
// "message <name> on whatever channel" through here.
type Contact struct {
	ID      int64           `json:"id,omitempty"`
	Name    string          `json:"name"`
	Note    string          `json:"note,omitempty"`
	Handles []ContactHandle `json:"handles,omitempty"`
}

// ContactHandle is one platform address for a contact.
type ContactHandle struct {
	ID        int64  `json:"id,omitempty"`
	ContactID int64  `json:"contact_id,omitempty"`
	Platform  string `json:"platform"` // 'telegram'|'whatsapp'|'discord'|'viber'
	Handle    string `json:"handle"`   // '@ali' | '+9607...' | discord id
	IsPrimary bool   `json:"is_primary,omitempty"`
}

// Response is the daemon's reply to a Request.
type Response struct {
	OK        bool         `json:"ok"`
	MessageID int64        `json:"message_id,omitempty"`
	ChatID    int64        `json:"chat_id,omitempty"`
	Reply     string       `json:"reply,omitempty"`
	Label     string       `json:"label,omitempty"`
	Messages  []Message    `json:"messages,omitempty"`
	Unread    []UnreadItem `json:"unread,omitempty"`
	Contacts  []Contact    `json:"contacts,omitempty"`
	Version   int          `json:"version,omitempty"` // daemon's ProtocolVersion (hello / mismatch reply)
	Error     string       `json:"error,omitempty"`
}
