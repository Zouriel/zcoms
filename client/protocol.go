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
	Op       string `json:"op"`                 // send|sendfile|read|ask|unread|mark_read|resolve|connectors|hello|contact_*|group_*|topic_create|topic_list|edit|pin|unpin|unpin_all|send_keep|delete|disappear|disappear_get|errand_*
	To       string `json:"to,omitempty"`       // @username or numeric chat id / errand target / transport-native id
	Text     string `json:"text,omitempty"`     // message body / question / caption
	Path     string `json:"path,omitempty"`     // local file path (sendfile)
	Count    int    `json:"count,omitempty"`    // history messages (read)
	Download bool   `json:"download,omitempty"` // download media in a read

	// Transport selects which connector a send/sendfile routes through:
	// "telegram" | "whatsapp". Absent = "telegram" (so every
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

	// Group ops and group-aware sends. ReplyTo is the message id to quote
	// (Telegram numeric id as a string, WhatsApp stanza id). Mentions are
	// member platform ids to tag (Telegram user id, WhatsApp JID).
	ReplyTo  string   `json:"reply_to,omitempty"`
	Mentions []string `json:"mentions,omitempty"`
	Name     string   `json:"name,omitempty"`    // group name (group_create)
	Members  []string `json:"members,omitempty"` // member names/addresses (group_create, group_add)

	// group_read window: Limit messages, latest by default, or Around/Before a
	// message id, or Since a unix time (seconds).
	Limit  int    `json:"limit,omitempty"`
	Around string `json:"around,omitempty"`
	Before string `json:"before,omitempty"`
	Since  int64  `json:"since,omitempty"`

	// Thread is the thread to post in on send/sendfile: a Telegram forum
	// topic id, or a topic name the daemon looks up. On WhatsApp (no
	// threads) it is dropped and the response sets ThreadIgnored.
	Thread string `json:"thread,omitempty"`

	// Topics creates the group with threads on (group_create, Telegram).
	Topics bool `json:"topics,omitempty"`

	// group_set flags; nil leaves the flag unchanged.
	Listen *bool `json:"listen,omitempty"`
	Follow *bool `json:"follow,omitempty"`

	// edit/pin/unpin (Telegram): the message to act on in chat To. Zero means
	// the newest message this account sent there. Silent pins without the
	// notification; ForSelf pins only on this side (private chats).
	MessageID int64 `json:"message_id,omitempty"`
	Silent    bool  `json:"silent,omitempty"`
	ForSelf   bool  `json:"for_self,omitempty"`

	// Disappear is the disappearing-messages timer in seconds (0 = off) for
	// disappear and group_create (Telegram; one timer per chat, threads share
	// it). Pin on send_keep pins the sent message.
	Disappear int32 `json:"disappear,omitempty"`
	Pin       bool  `json:"pin,omitempty"`
}

// Group is one row of the comms.db group registry. NativeID is the Telegram
// chat id (as a string) or the WhatsApp @g.us JID. Listen means calls are
// forwarded to the agent; Follow means every message is stored and readable by
// date.
type Group struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	NativeID  string   `json:"native_id"`
	Aliases   []string `json:"aliases,omitempty"`
	Listen    bool     `json:"listen"`
	Follow    bool     `json:"follow"`
}

// Topic is one thread of a group (a Telegram forum topic). ID is the topic id
// as a string, usable as Request.Thread.
type Topic struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// GroupMember is one member of a group. ID is the Telegram user id (as a
// string) or the WhatsApp JID.
type GroupMember struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username,omitempty"`
	IsSelf   bool   `json:"is_self,omitempty"`
	// IsOwner marks the member who is config.json's owner on this transport.
	IsOwner bool `json:"is_owner,omitempty"`
}

// GroupMessage is one message returned by group_read, in time order.
type GroupMessage struct {
	ID         string `json:"id"`
	SenderID   string `json:"sender_id"`
	SenderName string `json:"sender_name"`
	Text       string `json:"text"`
	Kind       string `json:"kind"`
	Date       int64  `json:"date"`
	Outgoing   bool   `json:"outgoing,omitempty"`
	ReplyToID  string `json:"reply_to_id,omitempty"`
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

	// DisappearIn is how many seconds are left before the message
	// disappears; 0 for a message that stays (Telegram).
	DisappearIn int64 `json:"disappear_in,omitempty"`
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

// Event kinds carried in Event.Event.
const (
	// EventMessage is an incoming message (1:1, or a group call).
	EventMessage = "message"
	// EventGroupJoined says the connected account was added to (or joined, or
	// created) a group. Address is the group's native id and GroupName its
	// name; Group is set so subscribers that do not handle groups drop it.
	// The daemon has already recorded the group in its registry.
	EventGroupJoined = "group_joined"
)

// Event is one pushed message on a subscribe stream: an incoming 1:1 message the
// daemon routed to this subscriber (by role/module name), a group call, or a
// group_joined notice.
type Event struct {
	Event string `json:"event"` // EventMessage | EventGroupJoined

	// Transport names the connector the message arrived on ("telegram" |
	// "whatsapp"). Address is the transport-native conversation id to reply to
	// (a WhatsApp JID; for Telegram it's the
	// chat id as a string, with ChatID/UserID also set for back-compat). Absent
	// Transport = a pre-v2 daemon, treat as "telegram".
	Transport string `json:"transport,omitempty"`
	Address   string `json:"address,omitempty"`

	ChatID    int64  `json:"chat_id"`
	UserID    int64  `json:"user_id"`
	Sender    string `json:"sender"`
	Text      string `json:"text"`
	Kind      string `json:"kind"`              // content type tag, e.g. tdlib "messageText"
	File      string `json:"file,omitempty"`    // local path if media was downloaded
	MessageID int64  `json:"message_id"`        // Telegram numeric id (0 for non-numeric transports)
	MsgRef    string `json:"msg_ref,omitempty"` // transport-native message id as a string (WhatsApp, …)
	Date      int64  `json:"date"`

	// FromSelf is true when the connected account itself sent this (own
	// outbound / notes-to-self). Triage drops these so it never re-ingests your
	// own traffic across any transport.
	FromSelf bool `json:"from_self,omitempty"`

	// Group calls. For a group event Address/ChatID name the GROUP and
	// MessageID/MsgRef the calling message; SenderID is the person (Telegram
	// user id or WhatsApp JID). Call is "mention" or "reply". Only calls from
	// listen=1 groups are emitted; 1:1 events leave these empty. Subscribers
	// that do not handle groups must drop events with Group set.
	Group     bool   `json:"group,omitempty"`
	GroupName string `json:"group_name,omitempty"`
	SenderID  string `json:"sender_id,omitempty"`
	ReplyToID string `json:"reply_to_id,omitempty"`
	Call      string `json:"call,omitempty"`

	// ThreadID is the thread (Telegram forum topic id) a group call was made
	// in; send the answer with Request.Thread set to it. Empty outside one.
	ThreadID string `json:"thread_id,omitempty"`

	// ReplyFile is the local path of the media in the message a call quotes
	// (someone replies to a photo and @mentions the account). File holds the
	// calling message's own media. Only calls download group media.
	ReplyFile string `json:"reply_file,omitempty"`

	// SenderIsOwner is set by the daemon on a group call whose sender is
	// config.json's owner on this transport (resolved through contacts), so
	// the agent tier can give the owner their fixed role without trusting
	// anything in the message.
	SenderIsOwner bool `json:"sender_is_owner,omitempty"`
}

// Contact is a person in the comms-owned contacts directory (comms.db). It is
// addressing data — every tier resolves "message <name> on whatever channel"
// through here. Fields are explicit per channel rather than a generic handle
// list: Phone is the universal number that addresses Telegram and WhatsApp; the
// per-platform ids override it where a contact's handle differs.
type Contact struct {
	ID       int64    `json:"id,omitempty"`
	Name     string   `json:"name"`
	Aliases  []string `json:"aliases,omitempty"`  // alternate names; resolvable, unique across all names+aliases
	Phone    string   `json:"phone,omitempty"`    // mobile number; addresses Telegram/WhatsApp
	Email    string   `json:"email,omitempty"`    // contact info (not a messaging channel)
	Telegram string   `json:"telegram,omitempty"` // @handle or id; falls back to Phone
	WhatsApp string   `json:"whatsapp,omitempty"` // wa id/number; falls back to Phone
	Note     string   `json:"note,omitempty"`
}

// Address returns the contact's address on a platform: the platform-specific id
// when set, otherwise the Phone number, which reaches both Telegram and
// WhatsApp. Returns "" when the contact has no
// usable address there.
func (c Contact) Address(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "telegram":
		return firstNonEmpty(c.Telegram, c.Phone)
	case "whatsapp":
		return firstNonEmpty(c.WhatsApp, c.Phone)
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
	Groups      bool `json:"groups,omitempty"`
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
	OK         bool          `json:"ok"`
	MessageID  int64         `json:"message_id,omitempty"`
	MsgRef     string        `json:"msg_ref,omitempty"` // transport-native id of a sent message (any transport; quote it with reply_to)
	ChatID     int64         `json:"chat_id,omitempty"`
	Address    string        `json:"address,omitempty"` // transport-native id of the sent/resolved message
	Reply      string        `json:"reply,omitempty"`
	Label      string        `json:"label,omitempty"`
	Messages   []Message     `json:"messages,omitempty"`
	Unread     []UnreadItem  `json:"unread,omitempty"`
	Contacts   []Contact     `json:"contacts,omitempty"`
	Connectors []Connector   `json:"connectors,omitempty"`
	Version    int           `json:"version,omitempty"`   // daemon's ProtocolVersion (hello / mismatch reply)
	Disappear  int32         `json:"disappear,omitempty"` // a chat's (or thread's) disappearing-messages timer, seconds (disappear, disappear_get)
	Timers     []ThreadTimer `json:"timers,omitempty"`    // a group's per-thread timers (disappear_get)
	Error      string        `json:"error,omitempty"`

	// Group ops.
	Group         *Group         `json:"group,omitempty"`
	Groups        []Group        `json:"groups,omitempty"`
	Members       []GroupMember  `json:"members,omitempty"`
	GroupMessages []GroupMessage `json:"group_messages,omitempty"`

	// Thread ops: topic_create answers Topic, topic_list Topics.
	Topic  *Topic  `json:"topic,omitempty"`
	Topics []Topic `json:"topics,omitempty"`

	// ThreadIgnored says a send asked for a thread on a transport whose
	// groups have none, so it went to the chat itself.
	ThreadIgnored bool `json:"thread_ignored,omitempty"`
}

// ThreadTimer is one thread's disappearing timer. Telegram keeps one timer per
// chat, so the daemon enforces these itself: it deletes the thread's messages
// once they are older than Seconds, never pinned ones, and only messages sent
// after the timer was set.
type ThreadTimer struct {
	ThreadID string `json:"thread_id"`
	Name     string `json:"name,omitempty"`
	Seconds  int32  `json:"seconds"`
}
