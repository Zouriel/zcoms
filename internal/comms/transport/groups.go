package transport

import "time"

// Call kinds carried in Inbound.Call.
const (
	CallMention = "mention"
	CallReply   = "reply"
)

// GroupFlags is how the owner configured a group in the comms.db registry.
// Listen forwards calls; Follow stores every message. The zero value (an
// unknown group) is off: no calls, nothing stored.
type GroupFlags struct {
	Name   string
	Listen bool
	Follow bool
}

// GroupPolicy looks up a group's flags by transport name and native id.
type GroupPolicy func(transportName, nativeID string) GroupFlags

// GroupPolicySetter is implemented by a transport that needs the registry to
// decide what to do with group traffic (WhatsApp, which stores its own
// history). The hub hands it the lookup before Start.
type GroupPolicySetter interface {
	SetGroupPolicy(p GroupPolicy)
}

// GroupStoreTrimmer is implemented by a transport that stores group messages
// itself (WhatsApp). The hub calls it after the owner changes a group's flags
// so stored rows shrink to what the new flags allow at once.
type GroupStoreTrimmer interface {
	TrimGroupStore(nativeID string, f GroupFlags) error
}

// GroupInfo identifies one group on a transport.
type GroupInfo struct {
	NativeID string
	Name     string
}

// GroupMember is one member of a group. ID is the Telegram user id or the
// WhatsApp JID.
type GroupMember struct {
	ID       string
	Name     string
	Username string
	IsSelf   bool
	Alt      string // the member's other address (WhatsApp phone JID of a LID member)
}

// GroupMessage is one message of a group window, returned oldest-first.
type GroupMessage struct {
	ID         string
	SenderID   string
	SenderName string
	Text       string
	Kind       string
	At         time.Time
	Outgoing   bool
	ReplyToID  string
}

// GroupQuery selects a window of group history. With Around set, Limit
// messages before and Limit after it (plus the message itself); with Before,
// Limit messages older than it; with Since, everything at or after that time
// (bounded by Limit); otherwise the latest Limit.
type GroupQuery struct {
	Limit  int
	Around string
	Before string
	Since  time.Time
}

// Grouper is an optional capability: a transport that can create, list and
// read groups. Member addresses are native user addresses (a Telegram user id
// as a string, a WhatsApp JID); the hub resolves names before calling.
type Grouper interface {
	CreateGroup(name string, members []string) (GroupInfo, error)
	AddGroupMembers(group string, members []string) error
	Groups() ([]GroupInfo, error)
	GroupMembers(group string) ([]GroupMember, error)
	GroupHistory(group string, q GroupQuery) ([]GroupMessage, error)
}

// SendOptions are the extras of a group-aware send: the message id to quote
// and the member ids to tag.
type SendOptions struct {
	ReplyTo  string
	Mentions []string
}

// RichSender is an optional capability: a transport that can quote a message
// and tag members when sending.
type RichSender interface {
	SendWith(to Address, text string, o SendOptions) (MsgRef, error)
}
