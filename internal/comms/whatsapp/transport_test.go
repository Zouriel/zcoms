package whatsapp

import (
	"testing"

	"github.com/Zouriel/zcoms/internal/comms/transport"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestMessageText(t *testing.T) {
	cases := []struct {
		name string
		msg  *waE2E.Message
		want string
	}{
		{"nil", nil, ""},
		{"conversation", &waE2E.Message{Conversation: proto.String("hi")}, "hi"},
		{"extended", &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String("link msg")}}, "link msg"},
		{"image caption", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: proto.String("a pic")}}, "a pic"},
		{"document caption", &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Caption: proto.String("a file")}}, "a file"},
		{"empty", &waE2E.Message{}, ""},
	}
	for _, c := range cases {
		if got := messageText(c.msg); got != c.want {
			t.Errorf("%s: messageText = %q, want %q", c.name, got, c.want)
		}
	}
}

// CurrentQR must only return a payload while in action_required/needs_qr, and
// must clear once any non-action state is entered (so a stale QR never lingers
// on the connectors page after pairing).
func TestQRStateMachine(t *testing.T) {
	tr := New("/tmp/does-not-matter.db")

	if tr.Status().State != transport.StateDisconnected {
		t.Fatalf("initial state = %q", tr.Status().State)
	}
	if tr.CurrentQR() != "" {
		t.Fatal("QR present before pairing flow")
	}

	tr.setQR("2@abc,def,==")
	if st := tr.Status(); st.State != transport.StateActionRequired || st.Detail != transport.NeedsQR {
		t.Fatalf("after setQR status = %+v", st)
	}
	if tr.CurrentQR() != "2@abc,def,==" {
		t.Fatalf("CurrentQR = %q", tr.CurrentQR())
	}

	tr.setStatus(transport.StateConnected, "")
	if tr.CurrentQR() != "" {
		t.Fatal("QR not cleared after connect")
	}
	if tr.Status().State != transport.StateConnected {
		t.Fatalf("state = %q", tr.Status().State)
	}
}

func TestCapsAndName(t *testing.T) {
	tr := New("/tmp/x.db")
	if tr.Name() != "whatsapp" {
		t.Fatalf("name = %q", tr.Name())
	}
	caps := tr.Caps()
	if !caps.Receive || !caps.Files {
		t.Fatalf("caps missing receive/files: %+v", caps)
	}
	if caps.BlockingAsk {
		t.Fatal("whatsapp must not advertise BlockingAsk")
	}
}
