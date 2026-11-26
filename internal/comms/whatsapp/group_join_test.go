package whatsapp

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestWhatsAppJoinNotices(t *testing.T) {
	tr, in := storeTransport(t, false, false)
	gid := types.NewJID("120363000000000009", types.GroupServer)

	tr.handleEvent(&events.JoinedGroup{GroupInfo: types.GroupInfo{JID: gid, GroupName: types.GroupName{Name: "Book club"}}})
	got, ok := received(in)
	if !ok || !got.Joined || got.Group || got.From.Transport != "whatsapp" || got.From.ID != gid.String() || got.GroupName != "Book club" {
		t.Fatalf("joined group: %+v %v", got, ok)
	}

	self := types.NewJID("9607000000", types.DefaultUserServer)
	tr.handleEvent(&events.GroupInfo{JID: gid, Join: []types.JID{self}})
	if got, ok := received(in); !ok || !got.Joined || got.From.ID != gid.String() {
		t.Fatalf("added by group info: %+v %v", got, ok)
	}

	other := types.NewJID("9607111111", types.DefaultUserServer)
	tr.handleEvent(&events.GroupInfo{JID: gid, Join: []types.JID{other}})
	if got, ok := received(in); ok {
		t.Fatalf("someone else joining produced %+v", got)
	}
}
