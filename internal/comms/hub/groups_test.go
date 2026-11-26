package hub

import (
	"strings"
	"testing"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"
	"github.com/Zouriel/zcoms/internal/comms/transport"
)

// groupDaemon is a test daemon with a registry holding one Telegram group.
func groupDaemon(t *testing.T, listen, follow bool, cs ...client.Contact) (*daemon, client.Group) {
	t.Helper()
	d := newDirectory(t, cs...)
	d.registry = map[string]transport.Transport{}
	d.inbound = make(chan transport.Inbound, 4)
	d.pendingAsk = map[int64][]chan string{}
	d.nameCache = map[int64]string{}
	d.subscribers = map[string][]chan client.Event{}
	g, err := d.contacts.UpsertGroup("telegram", "-1001234567890", "Acme build")
	if err != nil {
		t.Fatal(err)
	}
	g, err = d.contacts.SetGroupFlags(contacts.Owner, "telegram", g.NativeID, &listen, &follow)
	if err != nil {
		t.Fatal(err)
	}
	return d, g
}

func TestNegativeChatIDIsAGroup(t *testing.T) {
	d := newDirectory(t)
	chatID, userID, err := d.resolveChat("-1001234567890")
	if err != nil || chatID != -1001234567890 || userID != 0 {
		t.Fatalf("got (%d, %d, %v), want the chat with user id 0", chatID, userID, err)
	}
	chatID, userID, _ = d.resolveChat("5550001")
	if chatID != 5550001 || userID != 5550001 {
		t.Fatalf("private chat: got (%d, %d)", chatID, userID)
	}
}

func TestGroupNameResolvesForSend(t *testing.T) {
	d, _ := groupDaemon(t, false, false)
	chatID, userID, err := d.resolveChat("acme build")
	if err != nil || chatID != -1001234567890 || userID != 0 {
		t.Fatalf("got (%d, %d, %v)", chatID, userID, err)
	}
}

func TestNameMatchingContactAndGroupIsRefused(t *testing.T) {
	d, _ := groupDaemon(t, false, false, client.Contact{Name: "Acme build", Telegram: "@acmeperson"})
	_, _, err := d.resolveChat("Acme build")
	if err == nil || !strings.Contains(err.Error(), "say which one") {
		t.Fatalf("got %v, want a say-which-one error", err)
	}
}

func TestSeveralGroupsAreRefused(t *testing.T) {
	d, _ := groupDaemon(t, false, false)
	if _, err := d.contacts.UpsertGroup("telegram", "-1009", "Acme build"); err != nil {
		t.Fatal(err)
	}
	_, _, err := d.resolveChat("Acme build")
	if err == nil || !strings.Contains(err.Error(), "say which one") {
		t.Fatalf("got %v, want a say-which-one error", err)
	}
}

func TestWhatsAppGroupJIDPassesThrough(t *testing.T) {
	d := newDirectory(t)
	got, err := d.resolveWhatsApp("120363000000000001@g.us")
	if err != nil || got != "120363000000000001@g.us" {
		t.Fatalf("got (%q, %v)", got, err)
	}
	if _, err := d.contacts.UpsertGroup("whatsapp", "120363000000000001@g.us", "Family"); err != nil {
		t.Fatal(err)
	}
	got, err = d.resolveWhatsApp("family")
	if err != nil || got != "120363000000000001@g.us" {
		t.Fatalf("group name: got (%q, %v)", got, err)
	}
}

func TestAgentGroupReadRules(t *testing.T) {
	d, off := groupDaemon(t, false, false)
	q := transport.GroupQuery{Around: "42"}
	if err := checkGroupRead(d.contacts, "telegram", off, &q, true); err == nil {
		t.Fatal("agent read of an off group must be refused")
	}

	on := true
	g, _ := d.contacts.SetGroupFlags(contacts.Owner, "telegram", off.NativeID, &on, nil)

	q = transport.GroupQuery{Around: "42", Limit: 500}
	if err := checkGroupRead(d.contacts, "telegram", g, &q, true); err == nil {
		t.Fatal("around a message that was never a call must be refused")
	}
	if err := d.contacts.RecordCall("telegram", g.NativeID, "42", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := checkGroupRead(d.contacts, "telegram", g, &q, true); err != nil {
		t.Fatalf("around a recent call: %v", err)
	}
	if q.Limit != 50 {
		t.Fatalf("agent window not capped: %d", q.Limit)
	}

	for _, bad := range []transport.GroupQuery{{}, {Before: "42"}, {Since: time.Now().Add(-time.Hour)}} {
		bad := bad
		if err := checkGroupRead(d.contacts, "telegram", g, &bad, true); err == nil {
			t.Errorf("agent read %+v must be refused in a listen-only group", bad)
		}
	}

	g, _ = d.contacts.SetGroupFlags(contacts.Owner, "telegram", g.NativeID, nil, &on)
	q = transport.GroupQuery{Since: time.Now().Add(-24 * time.Hour)}
	if err := checkGroupRead(d.contacts, "telegram", g, &q, true); err != nil {
		t.Fatalf("since in a follow group: %v", err)
	}

	owner := transport.GroupQuery{}
	if err := checkGroupRead(d.contacts, "telegram", off, &owner, false); err != nil || owner.Limit != 50 {
		t.Fatalf("owner read: %v limit %d", err, owner.Limit)
	}
}

func TestGroupSetIsOwnerOnly(t *testing.T) {
	d, g := groupDaemon(t, false, false)
	on := true
	_, err := d.groupOp(client.Request{Op: "group_set", Caller: "agent", To: g.NativeID, Listen: &on})
	if err == nil {
		t.Fatal("group_set from the agent must be refused")
	}
	resp, err := d.groupOp(client.Request{Op: "group_set", To: g.NativeID, Listen: &on})
	if err != nil || resp.Group == nil || !resp.Group.Listen {
		t.Fatalf("owner group_set: %+v %v", resp.Group, err)
	}
}

const mentionUpdate = `{"@type":"updateNewMessage","message":{"id":7340033,"chat_id":-1001234567890,` +
	`"date":1764000000,"is_outgoing":false,"sender_id":{"@type":"messageSenderUser","user_id":5550001},` +
	`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"@zagent what's the eta?",` +
	`"entities":[{"@type":"textEntity","offset":0,"length":7,"type":{"@type":"textEntityTypeMention"}}]}}}}`

const mentionNameUpdate = `{"@type":"updateNewMessage","message":{"id":7340034,"chat_id":-1001234567890,` +
	`"date":1764000000,"is_outgoing":false,"sender_id":{"@type":"messageSenderUser","user_id":5550001},` +
	`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"hey Zed, status?",` +
	`"entities":[{"@type":"textEntity","offset":4,"length":3,"type":{"@type":"textEntityTypeMentionName","user_id":9990001}}]}}}}`

const replyUpdate = `{"@type":"updateNewMessage","message":{"id":7340035,"chat_id":-1001234567890,` +
	`"date":1764000000,"is_outgoing":false,"sender_id":{"@type":"messageSenderUser","user_id":5550001},` +
	`"reply_to":{"@type":"messageReplyToMessage","chat_id":-1001234567890,"message_id":1048576},` +
	`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"and tomorrow?","entities":[]}}}}`

const ownUpdate = `{"@type":"updateNewMessage","message":{"id":7340036,"chat_id":-1001234567890,` +
	`"date":1764000000,"is_outgoing":true,"sender_id":{"@type":"messageSenderUser","user_id":9990001},` +
	`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"@zagent note to self",` +
	`"entities":[{"@type":"textEntity","offset":0,"length":7,"type":{"@type":"textEntityTypeMention"}}]}}}}`

func dispatchOne(t *testing.T, d *daemon, update string, own func(int64, int64) bool) (transport.Inbound, bool) {
	t.Helper()
	d.me, d.meUsername = 9990001, "zagent"
	tr := newTelegramTransport(d)
	tr.ownMessage = own
	inbound := make(chan transport.Inbound, 1)
	tr.dispatchUpdate(update, inbound)
	select {
	case in := <-inbound:
		return in, true
	default:
		return transport.Inbound{}, false
	}
}

func TestTelegramGroupCalls(t *testing.T) {
	ownReply := func(chat, msg int64) bool { return msg == 1048576 }
	none := func(int64, int64) bool { return false }

	d, _ := groupDaemon(t, true, false)
	if in, ok := dispatchOne(t, d, plainGroupUpdate, ownReply); ok {
		t.Fatalf("plain message in a listen group produced %+v", in)
	}
	in, ok := dispatchOne(t, d, mentionUpdate, none)
	if !ok || in.Call != transport.CallMention || !in.Group || in.SenderID != "5550001" ||
		in.From.ID != "-1001234567890" || in.GroupName != "Acme build" || in.MessageID != "7340033" {
		t.Fatalf("@username mention: %+v %v", in, ok)
	}
	if in, ok := dispatchOne(t, d, mentionNameUpdate, none); !ok || in.Call != transport.CallMention {
		t.Fatalf("mention by name entity: %+v %v", in, ok)
	}
	in, ok = dispatchOne(t, d, replyUpdate, ownReply)
	if !ok || in.Call != transport.CallReply || in.ReplyToID != "1048576" {
		t.Fatalf("reply to own message: %+v %v", in, ok)
	}
	if in, ok := dispatchOne(t, d, replyUpdate, none); ok {
		t.Fatalf("reply to someone else's message produced %+v", in)
	}
	if in, ok := dispatchOne(t, d, ownUpdate, none); ok {
		t.Fatalf("own message produced %+v", in)
	}

	off, _ := groupDaemon(t, false, true)
	if in, ok := dispatchOne(t, off, mentionUpdate, none); ok {
		t.Fatalf("mention in a group that is not listened to produced %+v", in)
	}
}

func TestGroupCallEventIsRecorded(t *testing.T) {
	d, g := groupDaemon(t, true, false)
	ch := make(chan client.Event, 1)
	d.addSubscriber("agent", ch)
	d.deliverInbound(transport.Inbound{
		From: transport.Address{Transport: "telegram", ID: g.NativeID}, Sender: "@ibu", Text: "@zagent hi",
		MessageID: "42", At: time.Now(), Group: true, GroupName: g.Name, SenderID: "5550001", Call: transport.CallMention,
	})
	ev := <-ch
	if !ev.Group || ev.Call != "mention" || ev.Address != g.NativeID || ev.ChatID != -1001234567890 ||
		ev.SenderID != "5550001" || ev.MessageID != 42 || ev.UserID != 0 {
		t.Fatalf("event: %+v", ev)
	}
	if ok, _ := d.contacts.RecentCall("telegram", g.NativeID, "42", time.Hour); !ok {
		t.Fatal("call not recorded")
	}
}
