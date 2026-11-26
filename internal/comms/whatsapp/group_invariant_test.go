package whatsapp

import (
	"testing"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// groupMessage builds an incoming WhatsApp group text message.
func groupMessage(msg *waE2E.Message) *events.Message {
	group := types.NewJID("120363000000000001", types.GroupServer)
	sender := types.NewJID("9607000001", types.DefaultUserServer)
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: group, Sender: sender, IsGroup: true},
			ID:            "3EB0AAAA",
			PushName:      "Ibu",
			Timestamp:     time.Unix(1764000000, 0),
		},
		Message: msg,
	}
}

// The invariant every group phase keeps: a plain group message (no mention, no
// reply) never reaches subscribers.
func TestPlainWhatsAppGroupMessageProducesNoInbound(t *testing.T) {
	tr := New(t.TempDir() + "/x.db")
	inbound := make(chan transport.Inbound, 1)
	tr.inbound = inbound
	tr.me = "9607000000@s.whatsapp.net"

	tr.onMessage(groupMessage(&waE2E.Message{Conversation: proto.String("lunch at one?")}))

	select {
	case in := <-inbound:
		t.Fatalf("plain group message produced an inbound: %+v", in)
	default:
	}
}
