package hub

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/comms/transport"
)

var (
	_ transport.Grouper    = (*telegramTransport)(nil)
	_ transport.RichSender = (*telegramTransport)(nil)
)

// self returns the account's own user id and username.
func (d *daemon) self() (int64, string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.me, d.meUsername
}

// dispatchGroup handles a message in a non-private chat. Only a call (an
// @mention of the account, or a reply to one of its messages) in a listen
// group becomes an Inbound; everything else is dropped here, so a group that
// is off or a plain message never reaches a subscriber.
func (t *telegramTransport) dispatchGroup(msg telegram.Message, inbound chan<- transport.Inbound) {
	if msg.IsOutgoing || msg.SenderID.Type != "messageSenderUser" {
		return
	}
	me, username := t.d.self()
	if me != 0 && msg.SenderID.UserID == me {
		return
	}
	chat := strconv.FormatInt(msg.ChatID, 10)
	flags := t.d.groupFlags("telegram", chat)
	if !flags.Listen {
		return
	}

	text, entities := msg.Content.Text.Text, msg.Content.Text.Entities
	if msg.Content.Type != "messageText" {
		text, entities = msg.Content.Caption.Text, msg.Content.Caption.Entities
	}

	call := ""
	replyTo := ""
	if r := msg.ReplyTo; r != nil && r.Type == "messageReplyToMessage" && r.MessageID != 0 {
		replyTo = strconv.FormatInt(r.MessageID, 10)
	}
	switch {
	case telegram.MentionsUser(text, entities, me, username):
		call = transport.CallMention
	case replyTo != "" && (msg.ReplyTo.ChatID == 0 || msg.ReplyTo.ChatID == msg.ChatID) &&
		t.isOwnMessage(msg.ChatID, msg.ReplyTo.MessageID):
		call = transport.CallReply
	default:
		return
	}

	in := transport.Inbound{
		From:      transport.Address{Transport: "telegram", ID: chat},
		Sender:    t.d.senderName(msg.SenderID.UserID),
		Text:      replyText(msg.Content),
		Kind:      msg.Content.Type,
		MessageID: strconv.FormatInt(msg.ID, 10),
		At:        time.Unix(msg.Date, 0),
		Group:     true,
		GroupName: flags.Name,
		SenderID:  strconv.FormatInt(msg.SenderID.UserID, 10),
		ReplyToID: replyTo,
		Call:      call,
	}
	select {
	case inbound <- in:
	case <-time.After(5 * time.Second):
		t.d.logf("inbound channel full: dropped a telegram group call in %s", flags.Name)
	}
}

// isOwnMessage reports whether a message in a chat was sent by this account.
func (t *telegramTransport) isOwnMessage(chatID, messageID int64) bool {
	if t.ownMessage != nil {
		return t.ownMessage(chatID, messageID)
	}
	if t.d.tdjson == nil {
		return false
	}
	m, err := telegram.FetchMessage(t.d.tdjson, t.d.clientID, chatID, messageID)
	if err != nil {
		return false
	}
	me, _ := t.d.self()
	return m.IsOutgoing || (me != 0 && m.SenderID.Type == "messageSenderUser" && m.SenderID.UserID == me)
}

func parseUserIDs(members []string) ([]int64, error) {
	ids := make([]int64, 0, len(members))
	for _, m := range members {
		id, err := strconv.ParseInt(strings.TrimSpace(m), 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("%q is not a Telegram user id", m)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func groupChatID(group string) (int64, error) {
	id, ok := chatIDOf(strings.TrimSpace(group))
	if !ok || id >= 0 {
		return 0, fmt.Errorf("%q is not a Telegram group chat id", group)
	}
	return id, nil
}

// failedMembers names the users Telegram would not add.
func (t *telegramTransport) failedMembers(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	names := make([]string, 0, len(ids))
	for _, id := range ids {
		names = append(names, t.d.senderName(id))
	}
	return fmt.Errorf("telegram would not add %s (their privacy settings may block being added; send them an invite link)",
		strings.Join(names, ", "))
}

// CreateGroup creates a supergroup and adds the members.
func (t *telegramTransport) CreateGroup(name string, members []string) (transport.GroupInfo, error) {
	ids, err := parseUserIDs(members)
	if err != nil {
		return transport.GroupInfo{}, err
	}
	chatID, err := telegram.CreateSupergroup(t.d.tdjson, t.d.clientID, name)
	if err != nil {
		return transport.GroupInfo{}, err
	}
	info := transport.GroupInfo{NativeID: strconv.FormatInt(chatID, 10), Name: name}
	failed, err := telegram.AddChatMembers(t.d.tdjson, t.d.clientID, chatID, ids)
	if err != nil {
		return info, fmt.Errorf("group created but adding members failed: %w", err)
	}
	return info, t.failedMembers(failed)
}

// AddGroupMembers adds users to a group.
func (t *telegramTransport) AddGroupMembers(group string, members []string) error {
	chatID, err := groupChatID(group)
	if err != nil {
		return err
	}
	ids, err := parseUserIDs(members)
	if err != nil {
		return err
	}
	failed, err := telegram.AddChatMembers(t.d.tdjson, t.d.clientID, chatID, ids)
	if err != nil {
		return err
	}
	return t.failedMembers(failed)
}

// Groups lists the groups the account is in.
func (t *telegramTransport) Groups() ([]transport.GroupInfo, error) {
	chats, err := telegram.FetchGroupChats(t.d.tdjson, t.d.clientID)
	if err != nil {
		return nil, err
	}
	out := make([]transport.GroupInfo, 0, len(chats))
	for _, c := range chats {
		out = append(out, transport.GroupInfo{NativeID: strconv.FormatInt(c.ChatID, 10), Name: c.Title})
	}
	return out, nil
}

// GroupMembers lists a group's members.
func (t *telegramTransport) GroupMembers(group string) ([]transport.GroupMember, error) {
	chatID, err := groupChatID(group)
	if err != nil {
		return nil, err
	}
	ids, err := telegram.FetchGroupMemberIDs(t.d.tdjson, t.d.clientID, chatID)
	if err != nil {
		return nil, err
	}
	me, _ := t.d.self()
	out := make([]transport.GroupMember, 0, len(ids))
	for _, id := range ids {
		m := transport.GroupMember{ID: strconv.FormatInt(id, 10), IsSelf: id == me}
		if u, err := telegram.FetchUser(t.d.tdjson, t.d.clientID, id); err == nil {
			m.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
			if u.Username != "" {
				m.Username = "@" + u.Username
			}
		}
		if m.Name == "" {
			m.Name = m.ID
		}
		out = append(out, m)
	}
	return out, nil
}

// GroupHistory reads a window of a group's history from TDLib.
func (t *telegramTransport) GroupHistory(group string, q transport.GroupQuery) ([]transport.GroupMessage, error) {
	chatID, err := groupChatID(group)
	if err != nil {
		return nil, err
	}
	td, cid := t.d.tdjson, t.d.clientID
	var older, newer []telegram.Message
	var middle *telegram.Message
	switch {
	case q.Around != "":
		at, err := strconv.ParseInt(q.Around, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad message id %q", q.Around)
		}
		m, err := telegram.FetchMessage(td, cid, chatID, at)
		if err != nil {
			return nil, fmt.Errorf("message %s: %w", q.Around, err)
		}
		middle = &m
		if older, err = telegram.FetchOlder(td, cid, chatID, at, q.Limit, 0); err != nil {
			return nil, err
		}
		if newer, err = telegram.FetchNewer(td, cid, chatID, at, q.Limit); err != nil {
			return nil, err
		}
	case q.Before != "":
		at, err := strconv.ParseInt(q.Before, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("bad message id %q", q.Before)
		}
		if older, err = telegram.FetchOlder(td, cid, chatID, at, q.Limit, 0); err != nil {
			return nil, err
		}
	default:
		var since int64
		if !q.Since.IsZero() {
			since = q.Since.Unix()
		}
		if older, err = telegram.FetchOlder(td, cid, chatID, 0, q.Limit, since); err != nil {
			return nil, err
		}
	}

	out := make([]transport.GroupMessage, 0, len(older)+len(newer)+1)
	for i := len(older) - 1; i >= 0; i-- { // older is newest first
		out = append(out, t.groupMessage(older[i]))
	}
	if middle != nil {
		out = append(out, t.groupMessage(*middle))
	}
	for _, m := range newer {
		out = append(out, t.groupMessage(m))
	}
	return out, nil
}

func (t *telegramTransport) groupMessage(m telegram.Message) transport.GroupMessage {
	gm := transport.GroupMessage{
		ID:       strconv.FormatInt(m.ID, 10),
		Text:     replyText(m.Content),
		Kind:     m.Content.Type,
		At:       time.Unix(m.Date, 0),
		Outgoing: m.IsOutgoing,
	}
	switch m.SenderID.Type {
	case "messageSenderUser":
		gm.SenderID = strconv.FormatInt(m.SenderID.UserID, 10)
		gm.SenderName = t.d.senderName(m.SenderID.UserID)
	case "messageSenderChat":
		gm.SenderID = strconv.FormatInt(m.SenderID.ChatID, 10)
		gm.SenderName = gm.SenderID
		if title, err := telegram.FetchChatTitle(t.d.tdjson, t.d.clientID, m.SenderID.ChatID); err == nil && title != "" {
			gm.SenderName = title
		}
	}
	if r := m.ReplyTo; r != nil && r.Type == "messageReplyToMessage" && r.MessageID != 0 {
		gm.ReplyToID = strconv.FormatInt(r.MessageID, 10)
	}
	return gm
}

// SendWith sends text quoting o.ReplyTo and tagging o.Mentions (user ids).
// Only the first part of a long message carries the quote and the tags.
func (t *telegramTransport) SendWith(to transport.Address, text string, o transport.SendOptions) (transport.MsgRef, error) {
	chatID, _, err := t.d.resolveChat(to.ID)
	if err != nil {
		return transport.MsgRef{}, err
	}
	var opts telegram.SendOptions
	if o.ReplyTo != "" {
		if opts.ReplyTo, err = strconv.ParseInt(o.ReplyTo, 10, 64); err != nil {
			return transport.MsgRef{}, fmt.Errorf("bad reply_to message id %q", o.ReplyTo)
		}
	}
	ids, err := parseUserIDs(o.Mentions)
	if err != nil {
		return transport.MsgRef{}, err
	}
	for _, id := range ids {
		m := telegram.Mention{UserID: id}
		if u, err := telegram.FetchUser(t.d.tdjson, t.d.clientID, id); err == nil {
			m.Name = strings.TrimSpace(u.FirstName + " " + u.LastName)
			m.Username = u.Username
		}
		if m.Name == "" {
			m.Name = strconv.FormatInt(id, 10)
		}
		opts.Mentions = append(opts.Mentions, m)
	}

	var last int64
	for i, part := range chunk(text, telegramMaxLen) {
		var id int64
		if i == 0 {
			id, err = telegram.SendTextMessageWith(t.d.tdjson, t.d.clientID, chatID, part, opts)
		} else {
			id, err = telegram.SendTextMessage(t.d.tdjson, t.d.clientID, chatID, part)
		}
		if err != nil {
			return transport.MsgRef{}, err
		}
		last = id
	}
	return transport.MsgRef{ID: strconv.FormatInt(last, 10), ChatID: strconv.FormatInt(chatID, 10)}, nil
}
