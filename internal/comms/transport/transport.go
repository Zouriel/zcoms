// Package transport is the comms foundation's multi-transport abstraction. Every
// messaging channel the daemon speaks — Telegram (in-process TDLib), WhatsApp
// (in-process whatsmeow), Instagram (Python sidecar over HTTP), and any future
// connector — implements Transport. The hub keeps them in a registry keyed by
// Name() and routes sends by Address.Transport; inbound messages from every
// transport (push or poll) fan into one channel the hub broadcasts to
// subscribers. This is what lets auto-reply, triage, and the connectors page
// treat all apps uniformly instead of hard-coding Telegram.
package transport

import (
	"context"
	"time"
)

// Transport is one connected messaging account/channel. Implementations are
// either in-process (Telegram, WhatsApp) or thin clients over a sidecar
// (Instagram). Caps advertises what the transport can do so callers can gate
// behaviour (e.g. blocking ask only where BlockingAsk).
type Transport interface {
	Name() string // "telegram" | "whatsapp" | "instagram"
	Caps() Caps
	Status() ConnStatus
	Send(to Address, text string) (MsgRef, error)
	SendFile(to Address, path, caption string) (MsgRef, error)
	// Start runs the transport's receive loop, pushing every inbound message
	// onto the shared channel until ctx is cancelled. Push transports block on
	// their event stream; poll transports tick on the scheduler. Returns when
	// the loop stops.
	Start(ctx context.Context, inbound chan<- Inbound) error
	Stop() error
}

// Caps describes what a transport supports. BlockingAsk is the synchronous
// "send a question and wait for the reply on the same connection" mode that only
// Telegram offers; WhatsApp/Instagram get the async reply path instead.
type Caps struct {
	Receive     bool `json:"receive"`
	BlockingAsk bool `json:"blocking_ask"`
	Files       bool `json:"files"`
	Presence    bool `json:"presence"`
}

// ConnStatus is a transport's current connection state. For ActionRequired the
// Detail carries the specific sub-state the connectors UI must surface (a QR to
// scan, a code to enter, a 2FA/challenge prompt, …).
type ConnStatus struct {
	State  string    `json:"state"`            // see State* constants
	Detail string    `json:"detail,omitempty"` // see Needs* constants when State==ActionRequired
	Since  time.Time `json:"since"`
}

// Connection states.
const (
	StateDisconnected   = "disconnected"
	StateConnecting     = "connecting"
	StateActionRequired = "action_required"
	StateConnected      = "connected"
	StateError          = "error"
	StateSessionExpired = "session_expired"
)

// ActionRequired sub-states carried in ConnStatus.Detail.
const (
	NeedsQR        = "needs_qr"
	NeedsCode      = "needs_code"
	Needs2FA       = "needs_2fa"
	NeedsChallenge = "needs_challenge"
	NeedsPassword  = "needs_password"
)

// Address identifies a conversation on a specific transport. ID is the
// transport-native id as a string: a Telegram chat id, a WhatsApp JID, an
// Instagram thread/user id. Sends route on Transport; replies go back on the
// same Address the inbound arrived from.
type Address struct {
	Transport string `json:"transport"`
	ID        string `json:"id"`
}

// MsgRef is a best-effort handle to a just-sent message. Fields are strings so
// they hold any transport's id shape; empty where a transport can't supply one.
type MsgRef struct {
	ID     string // message id
	ChatID string // resolved conversation id (Telegram resolves @user → chat id)
	Label  string // human label for async sends (e.g. an upload's filename)
}

// QRProvider is an optional capability: a transport whose ActionRequired/needs_qr
// state has a QR payload to render (WhatsApp). The hub type-asserts for it when
// building connector status so the console can show the code to scan.
type QRProvider interface {
	CurrentQR() string
}

// HistMessage is one stored message returned by Reader.History (oldest-first).
type HistMessage struct {
	MessageID string
	Sender    string // display name, or "you" for own outbound
	FromMe    bool
	Text      string
	Kind      string
	File      string
	At        time.Time
}

// Reader is an optional capability: a transport that keeps a queryable message
// history so the daemon can serve `read`/`unread`/`mark_read` for it (Telegram
// reads live from TDLib; WhatsApp-over-whatsmeow keeps its own store). Unread
// returns messages others sent that haven't been triaged yet, as Inbounds.
type Reader interface {
	History(chatID string, count int) ([]HistMessage, error)
	Unread() ([]Inbound, error)
	MarkRead(chatID string, msgIDs []string) error
}

// Actor is an optional capability: a transport that can run a named connect/
// disconnect action from the connectors page. Known actions:
//   - "reconnect": re-arm pairing (e.g. regenerate a fresh WhatsApp QR after one
//     expired) or reconnect a dropped session.
//   - "logout": sign the account out (drops the stored session).
type Actor interface {
	Action(name string) error
}

// Inbound is one received message, normalised across transports. FromSelf marks
// messages the connected account itself sent (own outbound / notes-to-self) so
// triage can drop them in one place for every transport.
type Inbound struct {
	From      Address
	FromSelf  bool
	Sender    string    // transport-resolved sender handle/name (Telegram @username, WA push name, …)
	Text      string
	Kind      string    // content type tag (e.g. tdlib "messageText")
	Files     []string  // local paths to any downloaded attachments
	MessageID string    // transport message id
	At        time.Time
}
