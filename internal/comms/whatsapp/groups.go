package whatsapp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

var (
	_ transport.Grouper           = (*Transport)(nil)
	_ transport.RichSender        = (*Transport)(nil)
	_ transport.FileSender        = (*Transport)(nil)
	_ transport.GroupPolicySetter = (*Transport)(nil)
	_ transport.GroupStoreTrimmer = (*Transport)(nil)
)

// Group storage limits (spec 2c).
const (
	followRetention = 30 * 24 * time.Hour // follow groups: every message, 30 days
	listenKeep      = 200                 // listen-only groups: rolling window
	groupOpTimeout  = 30 * time.Second
)

// SetGroupPolicy hands the transport the registry lookup. Without one every
// group is off: nothing stored, nothing emitted.
func (t *Transport) SetGroupPolicy(p transport.GroupPolicy) {
	t.mu.Lock()
	t.policy = p
	t.mu.Unlock()
}

func (t *Transport) groupFlags(chat string) transport.GroupFlags {
	t.mu.Lock()
	p := t.policy
	t.mu.Unlock()
	if p == nil {
		return transport.GroupFlags{}
	}
	return p("whatsapp", chat)
}

// selfUsers returns the account's own user parts: its phone number and, once
// known, its LID. A JID is ours when its user part is one of these.
func (t *Transport) selfUsers() []string {
	t.mu.Lock()
	me, c := t.me, t.client
	t.mu.Unlock()
	var out []string
	if j, err := types.ParseJID(me); err == nil && j.User != "" {
		out = append(out, j.User)
	}
	if c != nil && c.Store != nil {
		if lid := c.Store.GetLID(); !lid.IsEmpty() {
			out = append(out, lid.User)
		}
	}
	return out
}

func (t *Transport) isSelf(jid string) bool {
	j, err := types.ParseJID(jid)
	if err != nil || j.User == "" {
		return false
	}
	for _, u := range t.selfUsers() {
		if u == j.User {
			return true
		}
	}
	return false
}

// altOf renders a sender's alternative address, "" when there is none.
func altOf(j types.JID) string {
	if j.IsEmpty() {
		return ""
	}
	return j.ToNonAD().String()
}

// onGroupInfo emits a join notice when a group change lists this account
// among the members who joined or were added.
func (t *Transport) onGroupInfo(e *events.GroupInfo) {
	for _, j := range e.Join {
		if t.isSelf(j.String()) {
			name := ""
			if e.Name != nil {
				name = e.Name.Name
			}
			t.onJoined(e.JID, name)
			return
		}
	}
}

// onJoined tells the hub the account is now in group jid (added, joined by
// invite, or created it). The hub records the group and fills in a missing
// name; nothing about the group's messages changes here.
func (t *Transport) onJoined(jid types.JID, name string) {
	if jid.Server != types.GroupServer || t.inbound == nil {
		return
	}
	in := transport.Inbound{
		From:      transport.Address{Transport: "whatsapp", ID: jid.ToNonAD().String()},
		Joined:    true,
		GroupName: strings.TrimSpace(name),
		At:        time.Now(),
	}
	select {
	case t.inbound <- in:
	case <-time.After(5 * time.Second):
	}
}

// contextInfo returns the ContextInfo of whichever message type carries one.
func contextInfo(m *waE2E.Message) *waE2E.ContextInfo {
	if m == nil {
		return nil
	}
	switch {
	case m.GetExtendedTextMessage() != nil:
		return m.GetExtendedTextMessage().GetContextInfo()
	case m.GetImageMessage() != nil:
		return m.GetImageMessage().GetContextInfo()
	case m.GetVideoMessage() != nil:
		return m.GetVideoMessage().GetContextInfo()
	case m.GetDocumentMessage() != nil:
		return m.GetDocumentMessage().GetContextInfo()
	case m.GetAudioMessage() != nil:
		return m.GetAudioMessage().GetContextInfo()
	case m.GetStickerMessage() != nil:
		return m.GetStickerMessage().GetContextInfo()
	}
	return nil
}

// groupCall decides whether a group message calls the account: it @mentions
// our phone JID or LID, or it replies to a message we sent.
func (t *Transport) groupCall(ci *waE2E.ContextInfo) string {
	if ci == nil {
		return ""
	}
	for _, j := range ci.GetMentionedJID() {
		if t.isSelf(j) {
			return transport.CallMention
		}
	}
	if ci.GetStanzaID() != "" && t.isSelf(ci.GetParticipant()) {
		return transport.CallReply
	}
	return ""
}

// onGroupMessage stores a group message as the registry says and emits a call
// from a listen group. A group that is neither listened to nor followed is
// ignored entirely: nothing stored, nothing emitted.
func (t *Transport) onGroupMessage(e *events.Message) {
	info := e.Info
	chat := info.Chat.ToNonAD().String()
	flags := t.groupFlags(chat)
	if !flags.Listen && !flags.Follow {
		return
	}

	kind := groupKind(e.Message)
	text := messageText(e.Message)
	storeText := text
	if storeText == "" && kind != "messageText" {
		storeText = "[" + strings.TrimPrefix(kind, "message") + "]"
	}
	sender := info.Sender.ToNonAD().String()
	fromMe := info.IsFromMe || t.isSelf(sender)
	ci := contextInfo(e.Message)
	name := info.PushName
	if name == "" {
		name = info.Sender.User
	}
	t.storeGroupMessage(chat, info.ID, name, sender, fromMe, storeText, kind, info.Timestamp, ci.GetStanzaID(), flags.Follow)

	if fromMe || !flags.Listen || t.inbound == nil {
		return
	}
	call := t.groupCall(ci)
	if call == "" {
		return
	}
	// A call's own media, and the media of the message it quotes, are the
	// only group files ever downloaded.
	var files []string
	file, note := t.groupCallMedia(chat, info.ID, e.Message, false)
	if file != "" {
		files = []string{file}
	}
	replyFile, replyNote := t.groupCallMedia(chat, ci.GetStanzaID(), ci.GetQuotedMessage(), true)
	in := transport.Inbound{
		From:      transport.Address{Transport: "whatsapp", ID: chat},
		Sender:    name,
		Text:      withNotes(storeText, note, replyNote),
		Kind:      kind,
		Files:     files,
		ReplyFile: replyFile,
		MessageID: info.ID,
		At:        info.Timestamp,
		Group:     true,
		GroupName: flags.Name,
		SenderID:  sender,
		SenderAlt: altOf(info.SenderAlt),
		ReplyToID: ci.GetStanzaID(),
		Call:      call,
	}
	select {
	case t.inbound <- in:
	case <-time.After(5 * time.Second):
	}
}

// storeGroupMessage records one group message, never as unread, and trims the
// chat: 30 days for a follow group, the latest 200 otherwise.
func (t *Transport) storeGroupMessage(chat, msgID, sender, senderJID string, fromMe bool, text, kind string, ts time.Time, replyTo string, follow bool) {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	fm := 0
	if fromMe {
		fm = 1
	}
	_, _ = db.Exec(`INSERT OR IGNORE INTO zc_messages(chat_jid, msg_id, sender, from_me, text, kind, ts, unread, is_group, sender_jid, reply_to)
		VALUES(?,?,?,?,?,?,?,0,1,?,?)`,
		chat, msgID, sender, fm, text, kind, ts.Unix(), nullStr(senderJID), nullStr(replyTo))
	_ = trimGroupRows(db, chat, transport.GroupFlags{Listen: !follow, Follow: follow})
}

// trimGroupRows cuts a group's stored rows down to what its flags allow:
// nothing for an off group, 30 days for a follow group, the latest 200 for a
// listen-only group.
func trimGroupRows(db *sql.DB, chat string, f transport.GroupFlags) error {
	switch {
	case f.Follow:
		_, err := db.Exec(`DELETE FROM zc_messages WHERE chat_jid=? AND is_group=1 AND ts < ?`,
			chat, time.Now().Add(-followRetention).Unix())
		return err
	case f.Listen:
		_, err := db.Exec(`DELETE FROM zc_messages WHERE chat_jid=? AND is_group=1 AND id NOT IN (
			SELECT id FROM zc_messages WHERE chat_jid=? AND is_group=1 ORDER BY ts DESC, id DESC LIMIT ?)`,
			chat, chat, listenKeep)
		return err
	}
	_, err := db.Exec(`DELETE FROM zc_messages WHERE chat_jid=? AND is_group=1`, chat)
	return err
}

// TrimGroupStore applies a group's new flags to its stored rows at once, so
// clearing listen or follow does not leave history behind.
func (t *Transport) TrimGroupStore(chat string, f transport.GroupFlags) error {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return nil // nothing stored while the store is closed
	}
	return trimGroupRows(db, chat, f)
}

// sweepGroupStore applies the registry's current flags to every group that
// has stored rows. It catches flags changed while the daemon was down and
// follow rows aging past 30 days in a quiet group.
func (t *Transport) sweepGroupStore() error {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return nil
	}
	rows, err := db.Query(`SELECT DISTINCT chat_jid FROM zc_messages WHERE is_group=1`)
	if err != nil {
		return err
	}
	var chats []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return err
		}
		chats = append(chats, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range chats {
		if err := trimGroupRows(db, c, t.groupFlags(c)); err != nil {
			return fmt.Errorf("trimming %s: %w", c, err)
		}
	}
	return nil
}

// sweepEvery runs sweepGroupStore until ctx ends. Start launches it and its
// ctx stops it.
func (t *Transport) sweepEvery(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			_ = t.sweepGroupStore()
		}
	}
}

// storeOwnGroupSend records our own outbound to a group, as the registry says.
func (t *Transport) storeOwnGroupSend(chat, msgID, text, kind string, ts time.Time, replyTo string) {
	flags := t.groupFlags(chat)
	if !flags.Listen && !flags.Follow {
		return
	}
	t.mu.Lock()
	me := t.me
	t.mu.Unlock()
	if j, err := types.ParseJID(me); err == nil {
		me = j.ToNonAD().String()
	}
	t.storeGroupMessage(chat, msgID, "you", me, true, text, kind, ts, replyTo, flags.Follow)
}

func isGroupJID(s string) bool { return strings.HasSuffix(s, "@"+types.GroupServer) }

func parseJIDs(addrs []string) ([]types.JID, error) {
	out := make([]types.JID, 0, len(addrs))
	for _, a := range addrs {
		j, err := types.ParseJID(JID(a))
		if err != nil {
			return nil, fmt.Errorf("bad whatsapp jid %q: %w", a, err)
		}
		out = append(out, j)
	}
	return out, nil
}

func groupJID(group string) (types.JID, error) {
	j, err := types.ParseJID(strings.TrimSpace(group))
	if err != nil || j.Server != types.GroupServer {
		return types.JID{}, fmt.Errorf("%q is not a whatsapp group jid", group)
	}
	return j, nil
}

// failedParticipants names participants WhatsApp refused.
func failedParticipants(ps []types.GroupParticipant) error {
	var failed []string
	for _, p := range ps {
		if p.Error != 0 {
			failed = append(failed, fmt.Sprintf("%s (error %d)", p.JID.User, p.Error))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("whatsapp would not add %s (their privacy settings may require an invite)", strings.Join(failed, ", "))
}

// CreateGroup creates a group with the members.
func (t *Transport) CreateGroup(name string, members []string) (transport.GroupInfo, error) {
	c, err := t.connected()
	if err != nil {
		return transport.GroupInfo{}, err
	}
	jids, err := parseJIDs(members)
	if err != nil {
		return transport.GroupInfo{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), groupOpTimeout)
	defer cancel()
	info, err := c.CreateGroup(ctx, whatsmeow.ReqCreateGroup{Name: name, Participants: jids})
	if err != nil {
		return transport.GroupInfo{}, err
	}
	gi := transport.GroupInfo{NativeID: info.JID.String(), Name: info.Name}
	if gi.Name == "" {
		gi.Name = name
	}
	return gi, failedParticipants(info.Participants)
}

// AddGroupMembers adds members to a group.
func (t *Transport) AddGroupMembers(group string, members []string) error {
	c, err := t.connected()
	if err != nil {
		return err
	}
	g, err := groupJID(group)
	if err != nil {
		return err
	}
	jids, err := parseJIDs(members)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), groupOpTimeout)
	defer cancel()
	ps, err := c.UpdateGroupParticipants(ctx, g, jids, whatsmeow.ParticipantChangeAdd)
	if err != nil {
		return err
	}
	return failedParticipants(ps)
}

// Groups lists the groups the account is in.
func (t *Transport) Groups() ([]transport.GroupInfo, error) {
	c, err := t.connected()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), groupOpTimeout)
	defer cancel()
	infos, err := c.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]transport.GroupInfo, 0, len(infos))
	for _, gi := range infos {
		if gi.IsParent {
			continue // a community shell, not a chat
		}
		out = append(out, transport.GroupInfo{NativeID: gi.JID.String(), Name: gi.Name})
	}
	return out, nil
}

// GroupMembers lists a group's members.
func (t *Transport) GroupMembers(group string) ([]transport.GroupMember, error) {
	c, err := t.connected()
	if err != nil {
		return nil, err
	}
	g, err := groupJID(group)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), groupOpTimeout)
	defer cancel()
	info, err := c.GetGroupInfo(ctx, g)
	if err != nil {
		return nil, err
	}
	out := make([]transport.GroupMember, 0, len(info.Participants))
	for _, p := range info.Participants {
		m := transport.GroupMember{ID: p.JID.String(), IsSelf: t.isSelf(p.JID.String())}
		if !p.PhoneNumber.IsEmpty() && p.PhoneNumber.String() != m.ID {
			m.Alt = p.PhoneNumber.ToNonAD().String()
		}
		if !p.PhoneNumber.IsEmpty() && t.isSelf(p.PhoneNumber.String()) {
			m.IsSelf = true
		}
		if ci, err := c.Store.Contacts.GetContact(ctx, p.JID); err == nil && ci.Found {
			m.Name = firstOf(ci.FullName, ci.FirstName, ci.PushName, ci.BusinessName)
		}
		if m.Name == "" && !p.PhoneNumber.IsEmpty() {
			if ci, err := c.Store.Contacts.GetContact(ctx, p.PhoneNumber); err == nil && ci.Found {
				m.Name = firstOf(ci.FullName, ci.FirstName, ci.PushName, ci.BusinessName)
			}
		}
		if m.Name == "" {
			m.Name = firstOf(p.DisplayName, "+"+p.PhoneNumber.User, p.JID.User)
		}
		out = append(out, m)
	}
	return out, nil
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" && v != "+" {
			return v
		}
	}
	return ""
}

// GroupHistory reads a group window from the local store. Only listen and
// follow groups have one; for others it is empty.
func (t *Transport) GroupHistory(group string, q transport.GroupQuery) ([]transport.GroupMessage, error) {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return nil, errors.New("whatsapp message store not open")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	const cols = `id, COALESCE(msg_id,''), COALESCE(sender_jid,''), COALESCE(sender,''), from_me, COALESCE(text,''), COALESCE(kind,''), ts, COALESCE(reply_to,'')`
	anchor := func(msgID string) (int64, int64, error) {
		var id, ts int64
		err := db.QueryRow(`SELECT id, ts FROM zc_messages WHERE chat_jid=? AND msg_id=? AND is_group=1`, group, msgID).Scan(&id, &ts)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, fmt.Errorf("message %s is not in the stored history", msgID)
		}
		return id, ts, err
	}
	older := func(id, ts int64, n int) ([]transport.GroupMessage, error) {
		rows, err := db.Query(`SELECT `+cols+` FROM zc_messages WHERE chat_jid=? AND is_group=1 AND (ts < ? OR (ts = ? AND id < ?))
			ORDER BY ts DESC, id DESC LIMIT ?`, group, ts, ts, id, n)
		if err != nil {
			return nil, err
		}
		out, err := scanGroupRows(rows)
		reverse(out)
		return out, err
	}

	switch {
	case q.Around != "":
		id, ts, err := anchor(q.Around)
		if err != nil {
			return nil, err
		}
		before, err := older(id, ts, limit)
		if err != nil {
			return nil, err
		}
		rows, err := db.Query(`SELECT `+cols+` FROM zc_messages WHERE chat_jid=? AND is_group=1 AND (ts > ? OR (ts = ? AND id >= ?))
			ORDER BY ts ASC, id ASC LIMIT ?`, group, ts, ts, id, limit+1)
		if err != nil {
			return nil, err
		}
		after, err := scanGroupRows(rows)
		return append(before, after...), err
	case q.Before != "":
		id, ts, err := anchor(q.Before)
		if err != nil {
			return nil, err
		}
		return older(id, ts, limit)
	case !q.Since.IsZero():
		rows, err := db.Query(`SELECT `+cols+` FROM zc_messages WHERE chat_jid=? AND is_group=1 AND ts >= ?
			ORDER BY ts ASC, id ASC LIMIT ?`, group, q.Since.Unix(), limit)
		if err != nil {
			return nil, err
		}
		return scanGroupRows(rows)
	}
	return older(1<<62, 1<<62, limit)
}

func scanGroupRows(rows *sql.Rows) ([]transport.GroupMessage, error) {
	defer rows.Close()
	var out []transport.GroupMessage
	for rows.Next() {
		var m transport.GroupMessage
		var id, ts int64
		var fromMe int
		if err := rows.Scan(&id, &m.ID, &m.SenderID, &m.SenderName, &fromMe, &m.Text, &m.Kind, &ts, &m.ReplyToID); err != nil {
			return nil, err
		}
		m.Outgoing = fromMe != 0
		m.At = time.Unix(ts, 0)
		out = append(out, m)
	}
	return out, rows.Err()
}

func reverse(ms []transport.GroupMessage) {
	for i, j := 0, len(ms)-1; i < j; i, j = i+1, j-1 {
		ms[i], ms[j] = ms[j], ms[i]
	}
}

// quoteContext builds the ContextInfo quoting a stored message. ok is false
// when the message is no longer stored, so the send goes out without a quote.
func (t *Transport) quoteContext(chat, msgID string) (*waE2E.ContextInfo, bool) {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil || msgID == "" {
		return nil, false
	}
	var senderJID, text string
	var fromMe int
	err := db.QueryRow(`SELECT COALESCE(sender_jid,''), from_me, COALESCE(text,'') FROM zc_messages WHERE chat_jid=? AND msg_id=?`,
		chat, msgID).Scan(&senderJID, &fromMe, &text)
	if err != nil {
		return nil, false
	}
	if senderJID == "" && fromMe != 0 {
		t.mu.Lock()
		me := t.me
		t.mu.Unlock()
		if j, err := types.ParseJID(me); err == nil {
			senderJID = j.ToNonAD().String()
		}
	}
	if senderJID == "" && isGroupJID(chat) {
		return nil, false
	}
	if senderJID == "" {
		senderJID = chat // a 1:1 chat: the other party
	}
	return &waE2E.ContextInfo{
		StanzaID:      proto.String(msgID),
		Participant:   proto.String(senderJID),
		QuotedMessage: &waE2E.Message{Conversation: proto.String(text)},
	}, true
}

// withMentionTags makes sure each mentioned JID appears as "@<user>" in the
// text, which is how WhatsApp clients render a tag.
func withMentionTags(text string, jids []types.JID) string {
	for _, j := range jids {
		tag := "@" + j.User
		if !strings.Contains(text, tag) {
			if text != "" && !strings.HasSuffix(text, " ") && !strings.HasSuffix(text, "\n") {
				text += " "
			}
			text += tag
		}
	}
	return text
}

// SendWith sends text quoting o.ReplyTo (when still stored) and tagging
// o.Mentions (JIDs).
func (t *Transport) SendWith(to transport.Address, text string, o transport.SendOptions) (transport.MsgRef, error) {
	c, err := t.connected()
	if err != nil {
		return transport.MsgRef{}, err
	}
	jid, err := types.ParseJID(to.ID)
	if err != nil {
		return transport.MsgRef{}, fmt.Errorf("bad whatsapp jid %q: %w", to.ID, err)
	}
	mentions, err := parseJIDs(o.Mentions)
	if err != nil {
		return transport.MsgRef{}, err
	}
	msg := buildRichText(text, mentions, nil)
	replyTo := ""
	if o.ReplyTo != "" {
		if ci, ok := t.quoteContext(jid.String(), o.ReplyTo); ok {
			msg = buildRichText(text, mentions, ci)
			replyTo = o.ReplyTo
		}
	}
	resp, err := c.SendMessage(context.Background(), jid, msg)
	if err != nil {
		return transport.MsgRef{}, err
	}
	body := msg.GetExtendedTextMessage().GetText()
	if isGroupJID(jid.String()) {
		t.storeOwnGroupSend(jid.String(), resp.ID, body, "messageText", resp.Timestamp, replyTo)
	} else {
		t.storeMessage(jid.String(), resp.ID, "you", true, body, "messageText", "", resp.Timestamp, false)
	}
	return transport.MsgRef{ID: resp.ID, ChatID: jid.String()}, nil
}

// buildRichText builds an extended text message carrying mentions and an
// optional quote.
func buildRichText(text string, mentions []types.JID, ci *waE2E.ContextInfo) *waE2E.Message {
	if ci == nil {
		ci = &waE2E.ContextInfo{}
	}
	if len(mentions) > 0 {
		text = withMentionTags(text, mentions)
		for _, j := range mentions {
			ci.MentionedJID = append(ci.MentionedJID, j.String())
		}
	}
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text:        proto.String(text),
		ContextInfo: ci,
	}}
}
