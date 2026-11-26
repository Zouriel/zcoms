package whatsapp

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

const testGroup = "120363000000000001@g.us"

// storeTransport is a transport with an open message store, our own number set,
// and the test group configured with the given flags.
func storeTransport(t *testing.T, listen, follow bool) (*Transport, chan transport.Inbound) {
	t.Helper()
	tr := New(filepath.Join(t.TempDir(), "x.db"))
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "msgs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	tr.db = db
	if err := tr.initMessageStore(); err != nil {
		t.Fatal(err)
	}
	tr.me = "9607000000:12@s.whatsapp.net"
	tr.SetGroupPolicy(func(tn, id string) transport.GroupFlags {
		if tn == "whatsapp" && id == testGroup {
			return transport.GroupFlags{Name: "Family", Listen: listen, Follow: follow}
		}
		return transport.GroupFlags{}
	})
	in := make(chan transport.Inbound, 4)
	tr.inbound = in
	return tr, in
}

func text(s string, ci *waE2E.ContextInfo) *waE2E.Message {
	return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(s), ContextInfo: ci}}
}

func received(ch chan transport.Inbound) (transport.Inbound, bool) {
	select {
	case in := <-ch:
		return in, true
	default:
		return transport.Inbound{}, false
	}
}

func countRows(t *testing.T, tr *Transport) int {
	t.Helper()
	var n int
	if err := tr.db.QueryRow(`SELECT COUNT(*) FROM zc_messages WHERE chat_jid=?`, testGroup).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestWhatsAppGroupCalls(t *testing.T) {
	tr, ch := storeTransport(t, true, false)

	tr.onMessage(groupMessage(&waE2E.Message{Conversation: proto.String("lunch?")}))
	if in, ok := received(ch); ok {
		t.Fatalf("plain message produced %+v", in)
	}

	m := groupMessage(text("@9607000000 when is it?", &waE2E.ContextInfo{MentionedJID: []string{"9607000000@s.whatsapp.net"}}))
	m.Info.ID = "3EB0BBBB"
	tr.onMessage(m)
	in, ok := received(ch)
	if !ok || in.Call != transport.CallMention || !in.Group || in.From.ID != testGroup ||
		in.SenderID != "9607000001@s.whatsapp.net" || in.GroupName != "Family" || in.MessageID != "3EB0BBBB" {
		t.Fatalf("mention: %+v %v", in, ok)
	}

	r := groupMessage(text("and you?", &waE2E.ContextInfo{
		StanzaID: proto.String("3EB0MINE"), Participant: proto.String("9607000000@s.whatsapp.net"),
	}))
	r.Info.ID = "3EB0CCCC"
	tr.onMessage(r)
	if in, ok := received(ch); !ok || in.Call != transport.CallReply || in.ReplyToID != "3EB0MINE" {
		t.Fatalf("reply: %+v %v", in, ok)
	}

	other := groupMessage(text("+1", &waE2E.ContextInfo{
		StanzaID: proto.String("3EB0THEIRS"), Participant: proto.String("9607000002@s.whatsapp.net"),
	}))
	other.Info.ID = "3EB0DDDD"
	tr.onMessage(other)
	if in, ok := received(ch); ok {
		t.Fatalf("reply to someone else produced %+v", in)
	}

	own := groupMessage(text("@9607000000 me", &waE2E.ContextInfo{MentionedJID: []string{"9607000000@s.whatsapp.net"}}))
	own.Info.ID = "3EB0EEEE"
	own.Info.IsFromMe = true
	tr.onMessage(own)
	if in, ok := received(ch); ok {
		t.Fatalf("own message produced %+v", in)
	}

	if n := countRows(t, tr); n != 5 {
		t.Fatalf("listen group should store its messages, got %d rows", n)
	}
	unread, err := tr.Unread()
	if err != nil || len(unread) != 0 {
		t.Fatalf("group rows must never be unread: %+v %v", unread, err)
	}
}

func TestOffGroupStoresAndEmitsNothing(t *testing.T) {
	tr, ch := storeTransport(t, false, false)
	tr.onMessage(groupMessage(text("@9607000000 hi", &waE2E.ContextInfo{MentionedJID: []string{"9607000000@s.whatsapp.net"}})))
	if in, ok := received(ch); ok {
		t.Fatalf("off group produced %+v", in)
	}
	if n := countRows(t, tr); n != 0 {
		t.Fatalf("off group stored %d rows", n)
	}
}

func TestListenGroupKeepsRolling200(t *testing.T) {
	tr, _ := storeTransport(t, true, false)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 230; i++ {
		tr.storeGroupMessage(testGroup, "m"+string(rune('a'+i%26))+time.Duration(i).String(), "x", "1@s.whatsapp.net",
			false, "t", "messageText", base.Add(time.Duration(i)*time.Second), "", false)
	}
	if n := countRows(t, tr); n != listenKeep {
		t.Fatalf("rolling window kept %d rows, want %d", n, listenKeep)
	}
}

func TestFollowGroupKeeps30Days(t *testing.T) {
	tr, _ := storeTransport(t, true, true)
	tr.storeGroupMessage(testGroup, "old", "x", "1@s.whatsapp.net", false, "t", "messageText", time.Now().Add(-31*24*time.Hour), "", true)
	for i := 0; i < 230; i++ {
		tr.storeGroupMessage(testGroup, "n"+time.Duration(i).String(), "x", "1@s.whatsapp.net", false, "t", "messageText",
			time.Now().Add(-time.Duration(i)*time.Minute), "", true)
	}
	if n := countRows(t, tr); n != 230 {
		t.Fatalf("follow group kept %d rows, want 230 (all recent, the 31-day-old one gone)", n)
	}
}

func TestGroupHistoryWindow(t *testing.T) {
	tr, _ := storeTransport(t, true, true)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 20; i++ {
		tr.storeGroupMessage(testGroup, idOf(i), "x", "1@s.whatsapp.net", false, idOf(i), "messageText",
			base.Add(time.Duration(i)*time.Second), "", true)
	}
	got, err := tr.GroupHistory(testGroup, transport.GroupQuery{Around: idOf(10), Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range got {
		ids = append(ids, m.ID)
	}
	want := []string{idOf(7), idOf(8), idOf(9), idOf(10), idOf(11), idOf(12), idOf(13)}
	if len(ids) != len(want) {
		t.Fatalf("around window = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("around window = %v, want %v", ids, want)
		}
	}

	latest, _ := tr.GroupHistory(testGroup, transport.GroupQuery{Limit: 5})
	if len(latest) != 5 || latest[4].ID != idOf(19) || latest[0].ID != idOf(15) {
		t.Fatalf("latest = %+v", latest)
	}
	before, _ := tr.GroupHistory(testGroup, transport.GroupQuery{Before: idOf(5), Limit: 10})
	if len(before) != 5 || before[4].ID != idOf(4) {
		t.Fatalf("before = %+v", before)
	}
	since, _ := tr.GroupHistory(testGroup, transport.GroupQuery{Since: base.Add(15 * time.Second), Limit: 100})
	if len(since) != 5 || since[0].ID != idOf(15) {
		t.Fatalf("since = %+v", since)
	}
}

func idOf(i int) string { return "id" + string(rune('A'+i)) }

func TestQuoteContextAndMentions(t *testing.T) {
	tr, _ := storeTransport(t, true, false)
	tr.storeGroupMessage(testGroup, "Q1", "Ibu", "9607000001@s.whatsapp.net", false, "when?", "messageText", time.Now(), "", false)

	ci, ok := tr.quoteContext(testGroup, "Q1")
	if !ok || ci.GetStanzaID() != "Q1" || ci.GetParticipant() != "9607000001@s.whatsapp.net" ||
		ci.GetQuotedMessage().GetConversation() != "when?" {
		t.Fatalf("quote: %+v %v", ci, ok)
	}
	if _, ok := tr.quoteContext(testGroup, "GONE"); ok {
		t.Fatal("a message no longer stored must send without a quote")
	}

	j := types.NewJID("9607000001", types.DefaultUserServer)
	msg := buildRichText("on it", []types.JID{j}, ci)
	if got := msg.GetExtendedTextMessage().GetText(); got != "on it @9607000001" {
		t.Fatalf("text = %q", got)
	}
	if m := msg.GetExtendedTextMessage().GetContextInfo().GetMentionedJID(); len(m) != 1 || m[0] != j.String() {
		t.Fatalf("mentioned = %v", m)
	}
}
