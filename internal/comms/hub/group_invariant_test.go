package hub

import (
	"testing"

	"github.com/Zouriel/zcoms/internal/comms/transport"
)

// plainGroupUpdate is an incoming text message in a supergroup: no mention of
// the account and no reply to anything.
const plainGroupUpdate = `{"@type":"updateNewMessage","message":{"id":7340032,"chat_id":-1001234567890,` +
	`"date":1764000000,"is_outgoing":false,"sender_id":{"@type":"messageSenderUser","user_id":5550001},` +
	`"content":{"@type":"messageText","text":{"@type":"formattedText","text":"lunch at one?","entities":[]}}}}`

// The invariant every group phase keeps: a plain group message (no mention, no
// reply) never reaches subscribers.
func TestPlainTelegramGroupMessageProducesNoInbound(t *testing.T) {
	d := newTestDaemon()
	tr := newTelegramTransport(d)
	inbound := make(chan transport.Inbound, 1)

	tr.dispatchUpdate(plainGroupUpdate, inbound)

	select {
	case in := <-inbound:
		t.Fatalf("plain group message produced an inbound: %+v", in)
	default:
	}
}
