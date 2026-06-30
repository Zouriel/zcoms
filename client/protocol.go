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

import (
	"path/filepath"
	"strings"
)

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
	Op       string `json:"op"`                 // send|sendfile|read|ask|unread|mark_read|resolve|connectors|hello|contact_*|errand_*
	To       string `json:"to,omitempty"`       // @username or numeric chat id / errand target / transport-native id
	Text     string `json:"text,omitempty"`     // message body / question / caption
	Path     string `json:"path,omitempty"`     // local file path (sendfile)
	Count    int    `json:"count,omitempty"`    // history messages (read)
	Download bool   `json:"download,omitempty"` // download media in a read

	// Transport selects which connector a send/sendfile routes through:
	// "telegram" | "whatsapp" | "instagram". Absent = "telegram" (so every
	// pre-v2 single-id call keeps working unchanged).
	Transport string `json:"transport,omitempty"`

	// Protocol handshake: the client advertises the version it speaks. The
	// daemon rejects a mismatch (see version.go). Zero = legacy/unset; the
	// daemon treats absent as "no claim" so older callers keep working.
	Version int `json:"version,omitempty"`

	// Caller identity for store writes: "owner" (trusted: the CLI / console) or
	// "agent" (the running agent — untrusted for crown-jewel tables). Absent =
	// owner for backward-compatible local CLI calls.
	Caller string `json:"caller,omitempty"`

	// mark_read (Telegram uses ChatID+MessageIDs; non-Telegram transports use the
	// native address in To plus string MsgRefs).
	ChatID     int64    `json:"chat_id,omitempty"`
	MessageIDs []int64  `json:"message_ids,omitempty"`
	MsgRefs    []string `json:"msg_refs,omitempty"`

	// subscribe: which event stream to receive (a module/role name).
	Role string `json:"role,omitempty"`

	// Contacts store ops (comms.db). Contact carries the full row for
	// create/update/upsert (and just the id for delete).
	Contact *Contact `json:"contact,omitempty"`

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

	// Multi-transport: Telegram items leave these empty (Transport defaults to
	// telegram). Non-Telegram transports (WhatsApp, …) carry the transport name,
	// the native chat address (JID), and the string message id to mark read.
	Transport string `json:"transport,omitempty"`
	Address   string `json:"address,omitempty"`
	MsgRef    string `json:"msg_ref,omitempty"`
	File      string `json:"file,omitempty"`
}

// Event is one pushed message on a subscribe stream: an incoming 1:1 message the
// daemon routed to this subscriber (by role/module name).
type Event struct {
	Event string `json:"event"` // "message"

	// Transport names the connector the message arrived on ("telegram" |
	// "whatsapp" | "instagram"). Address is the transport-native conversation id
	// to reply to (a WhatsApp JID, an Instagram thread id; for Telegram it's the
	// chat id as a string, with ChatID/UserID also set for back-compat). Absent
	// Transport = a pre-v2 daemon, treat as "telegram".
	Transport string `json:"transport,omitempty"`
	Address   string `json:"address,omitempty"`

	ChatID    int64  `json:"chat_id"`
	UserID    int64  `json:"user_id"`
	Sender    string `json:"sender"`
	Text      string `json:"text"`
	Kind      string `json:"kind"`             // content type tag, e.g. tdlib "messageText"
	File      string `json:"file,omitempty"`   // local path if media was downloaded
	MessageID int64  `json:"message_id"`        // Telegram numeric id (0 for non-numeric transports)
	MsgRef    string `json:"msg_ref,omitempty"` // transport-native message id as a string (WhatsApp, …)
	Date      int64  `json:"date"`

	// FromSelf is true when the connected account itself sent this (own
	// outbound / notes-to-self). Triage drops these so it never re-ingests your
	// own traffic across any transport.
	FromSelf bool `json:"from_self,omitempty"`
}

// Contact is a person in the comms-owned contacts directory (comms.db). It is
// addressing data — every tier resolves "message <name> on whatever channel"
// through here. Fields are explicit per channel rather than a generic handle
// list: Phone is the universal number that addresses Telegram, WhatsApp and
// Viber; the per-platform ids override it where a contact's handle differs (or,
// for Discord, where there is no phone to fall back to). Discord and Viber are
// reserved for future transports — stored now, not yet routed.
type Contact struct {
	ID        int64    `json:"id,omitempty"`
	Name      string   `json:"name"`
	Aliases   []string `json:"aliases,omitempty"`   // alternate names; resolvable, unique across all names+aliases
	Phone     string   `json:"phone,omitempty"`     // mobile number; addresses Telegram/WhatsApp/Viber
	Email     string   `json:"email,omitempty"`     // contact info (not a messaging channel)
	Telegram  string   `json:"telegram,omitempty"`  // @handle or id; falls back to Phone
	WhatsApp  string   `json:"whatsapp,omitempty"`  // wa id/number; falls back to Phone
	Instagram string   `json:"instagram,omitempty"` // ig @handle; NO phone fallback (future)
	Discord   string   `json:"discord,omitempty"`   // discord id; NO phone fallback (future)
	Viber     string   `json:"viber,omitempty"`     // viber id; falls back to Phone (future)
	Github    string   `json:"github,omitempty"`    // github handle; contact info (not a messaging channel)
	Note      string   `json:"note,omitempty"`
}

// Address returns the contact's address on a platform: the platform-specific id
// when set, otherwise the Phone number — because a phone reaches Telegram,
// WhatsApp and Viber, but never Discord. Returns "" when the contact has no
// usable address there.
func (c Contact) Address(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "telegram":
		return firstNonEmpty(c.Telegram, c.Phone)
	case "whatsapp":
		return firstNonEmpty(c.WhatsApp, c.Phone)
	case "instagram":
		return c.Instagram // no phone fallback
	case "viber":
		return firstNonEmpty(c.Viber, c.Phone)
	case "discord":
		return c.Discord // no phone fallback
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// Caps mirrors transport.Caps on the wire: what a connector supports.
type Caps struct {
	Receive     bool `json:"receive"`
	BlockingAsk bool `json:"blocking_ask"`
	Files       bool `json:"files"`
	Presence    bool `json:"presence"`
}

// Connector is one transport's live status, returned by the "connectors" op and
// rendered as a card on the console's Connectors page. State is one of
// disconnected | connecting | action_required | connected | error |
// session_expired; when action_required, Detail names the action
// (needs_qr | needs_code | needs_2fa | needs_challenge | needs_password).
type Connector struct {
	Transport string `json:"transport"`
	State     string `json:"state"`
	Detail    string `json:"detail,omitempty"`
	Since     int64  `json:"since,omitempty"` // unix seconds the state was entered
	Caps      Caps   `json:"caps"`
	QR        string `json:"qr,omitempty"` // QR payload to render while Detail==needs_qr (WhatsApp)
}

// Response is the daemon's reply to a Request.
type Response struct {
	OK         bool         `json:"ok"`
	MessageID  int64        `json:"message_id,omitempty"`
	ChatID     int64        `json:"chat_id,omitempty"`
	Address    string       `json:"address,omitempty"` // transport-native id of the sent/resolved message
	Reply      string       `json:"reply,omitempty"`
	Label      string       `json:"label,omitempty"`
	Messages   []Message    `json:"messages,omitempty"`
	Unread     []UnreadItem `json:"unread,omitempty"`
	Contacts   []Contact    `json:"contacts,omitempty"`
	Connectors []Connector  `json:"connectors,omitempty"`
	Version    int          `json:"version,omitempty"` // daemon's ProtocolVersion (hello / mismatch reply)
	Error      string       `json:"error,omitempty"`
}
