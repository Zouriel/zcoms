package hub

import (
	"testing"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/transport"
)

const (
	addedSelfUpdate = `{"@type":"updateNewMessage","message":{"id":10,"chat_id":-1009876543210,"date":1700000000,` +
		`"is_outgoing":false,"sender_id":{"@type":"messageSenderUser","user_id":5550001},` +
		`"content":{"@type":"messageChatAddMembers","member_user_ids":[5550002,9990001]}}}`
	addedOtherUpdate = `{"@type":"updateNewMessage","message":{"id":11,"chat_id":-1009876543210,"date":1700000000,` +
		`"is_outgoing":false,"sender_id":{"@type":"messageSenderUser","user_id":5550001},` +
		`"content":{"@type":"messageChatAddMembers","member_user_ids":[5550002]}}}`
	basicCreateUpdate = `{"@type":"updateNewMessage","message":{"id":12,"chat_id":-4001,"date":1700000000,` +
		`"is_outgoing":false,"sender_id":{"@type":"messageSenderUser","user_id":5550001},` +
		`"content":{"@type":"messageBasicGroupChatCreate","title":"Trip","member_user_ids":[9990001,5550001]}}}`
	joinByLinkUpdate = `{"@type":"updateNewMessage","message":{"id":13,"chat_id":-1005555,"date":1700000000,` +
		`"is_outgoing":true,"sender_id":{"@type":"messageSenderUser","user_id":9990001},` +
		`"content":{"@type":"messageChatJoinByLink"}}}`
)

func dispatchJoinUpdate(t *testing.T, d *daemon, update string, isGroup bool) (transport.Inbound, bool) {
	t.Helper()
	d.me, d.meUsername = 9990001, "zagent"
	tr := newTelegramTransport(d)
	tr.ownMessage = func(int64, int64) bool { return false }
	tr.chatInfo = func(chatID int64) (bool, string, error) { return isGroup, "Chat title", nil }
	inbound := make(chan transport.Inbound, 1)
	tr.dispatchUpdate(update, inbound)
	select {
	case in := <-inbound:
		return in, true
	default:
		return transport.Inbound{}, false
	}
}

func TestTelegramJoinNotices(t *testing.T) {
	d := newTestDaemon()
	for _, u := range []string{addedSelfUpdate, basicCreateUpdate, joinByLinkUpdate} {
		in, ok := dispatchJoinUpdate(t, d, u, true)
		if !ok || !in.Joined || in.Group || in.From.Transport != "telegram" || in.GroupName != "Chat title" {
			t.Fatalf("join notice %s: %+v %v", u, in, ok)
		}
	}
	if in, ok := dispatchJoinUpdate(t, d, addedOtherUpdate, true); ok {
		t.Fatalf("someone else being added produced %+v", in)
	}
	if in, ok := dispatchJoinUpdate(t, d, joinByLinkUpdate, false); ok {
		t.Fatalf("joining a channel produced %+v", in)
	}
}

func TestGroupJoinedRegistersAndEmitsOnce(t *testing.T) {
	d, _ := groupDaemon(t, false, false)
	ch := make(chan client.Event, 4)
	d.addSubscriber("agent", ch)
	in := transport.Inbound{From: transport.Address{Transport: "telegram", ID: "-1009876543210"}, Joined: true, GroupName: "New crew", At: time.Now()}
	d.deliverInbound(in)
	d.deliverInbound(in) // the same join seen twice (create + platform notice)
	ev := <-ch
	if ev.Event != client.EventGroupJoined || !ev.Group || ev.Address != "-1009876543210" ||
		ev.GroupName != "New crew" || ev.ChatID != -1009876543210 || ev.MsgRef != "" {
		t.Fatalf("event: %+v", ev)
	}
	select {
	case ev := <-ch:
		t.Fatalf("second event for the same join: %+v", ev)
	default:
	}
	g, err := d.contacts.GroupByNative("telegram", "-1009876543210")
	if err != nil || g.Name != "New crew" || g.Listen || g.Follow {
		t.Fatalf("registry row: %+v %v", g, err)
	}

	// A join notice without a name keeps the name the registry already has.
	d.joinSeen = nil
	d.deliverInbound(transport.Inbound{From: transport.Address{Transport: "telegram", ID: "-1001234567890"}, Joined: true})
	ev = <-ch
	if ev.GroupName != "Acme build" {
		t.Fatalf("nameless join renamed the group: %+v", ev)
	}
}
