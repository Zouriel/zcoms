package hub

import (
	"context"
	"testing"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/transport"
)

// fakeTransport is an in-memory Transport for exercising the hub's registry
// routing, status reporting, and inbound fan-in without a live session.
type fakeTransport struct {
	name   string
	caps   transport.Caps
	status transport.ConnStatus
	sent   []struct{ to, text string }
}

func (f *fakeTransport) Name() string                { return f.name }
func (f *fakeTransport) Caps() transport.Caps        { return f.caps }
func (f *fakeTransport) Status() transport.ConnStatus { return f.status }
func (f *fakeTransport) Send(to transport.Address, text string) (transport.MsgRef, error) {
	f.sent = append(f.sent, struct{ to, text string }{to.ID, text})
	return transport.MsgRef{ID: "1", ChatID: to.ID}, nil
}
func (f *fakeTransport) SendFile(to transport.Address, path, caption string) (transport.MsgRef, error) {
	return transport.MsgRef{ChatID: to.ID, Label: path}, nil
}
func (f *fakeTransport) Start(ctx context.Context, inbound chan<- transport.Inbound) error { return nil }
func (f *fakeTransport) Stop() error                                                       { return nil }

func newTestDaemon() *daemon {
	return &daemon{
		registry:    map[string]transport.Transport{},
		inbound:     make(chan transport.Inbound, 16),
		pendingAsk:  map[int64][]chan string{},
		nameCache:   map[int64]string{},
		subscribers: map[string][]chan client.Event{},
	}
}

// connectors() must report each registered transport's state and caps.
func TestConnectorsSnapshot(t *testing.T) {
	d := newTestDaemon()
	d.registry["telegram"] = &fakeTransport{
		name:   "telegram",
		caps:   transport.Caps{Receive: true, BlockingAsk: true, Files: true},
		status: transport.ConnStatus{State: transport.StateConnected, Since: time.Now()},
	}
	d.registry["whatsapp"] = &fakeTransport{
		name:   "whatsapp",
		caps:   transport.Caps{Receive: true, Files: true},
		status: transport.ConnStatus{State: transport.StateActionRequired, Detail: transport.NeedsQR, Since: time.Now()},
	}

	cs := d.connectors()
	if len(cs) != 2 {
		t.Fatalf("want 2 connectors, got %d", len(cs))
	}
	// transportNames sorts, so telegram < whatsapp.
	if cs[0].Transport != "telegram" || cs[0].State != transport.StateConnected || !cs[0].Caps.BlockingAsk {
		t.Fatalf("telegram connector wrong: %+v", cs[0])
	}
	if cs[1].Transport != "whatsapp" || cs[1].State != transport.StateActionRequired || cs[1].Detail != transport.NeedsQR {
		t.Fatalf("whatsapp connector wrong: %+v", cs[1])
	}
	if cs[1].Caps.BlockingAsk {
		t.Fatal("whatsapp must not advertise BlockingAsk")
	}
}

// A non-telegram inbound must broadcast an Event carrying the transport tag and
// the native address to reply on (the Phase D reply path depends on this).
func TestDeliverInboundTagsTransport(t *testing.T) {
	d := newTestDaemon()
	ch := make(chan client.Event, 1)
	d.addSubscriber("bridge", ch)

	d.deliverInbound(transport.Inbound{
		From:      transport.Address{Transport: "whatsapp", ID: "9607654321@s.whatsapp.net"},
		Sender:    "9607654321",
		Text:      "hi",
		MessageID: "abc",
		At:        time.Now(),
	})

	select {
	case ev := <-ch:
		if ev.Transport != "whatsapp" || ev.Address != "9607654321@s.whatsapp.net" {
			t.Fatalf("event lost transport/address: %+v", ev)
		}
		if ev.ChatID != 0 {
			t.Fatalf("non-telegram event must not carry a numeric ChatID, got %d", ev.ChatID)
		}
		if ev.Text != "hi" {
			t.Fatalf("text wrong: %q", ev.Text)
		}
	case <-time.After(time.Second):
		t.Fatal("no event broadcast")
	}
}

// A telegram inbound populates the numeric ChatID/UserID for back-compat and
// resolves an outstanding blocking-ask instead of broadcasting it.
func TestDeliverInboundTelegramAskWins(t *testing.T) {
	d := newTestDaemon()
	sub := make(chan client.Event, 1)
	d.addSubscriber("bridge", sub)

	// No pending ask: a telegram message broadcasts with numeric ids set.
	d.deliverInbound(transport.Inbound{
		From:      transport.Address{Transport: "telegram", ID: "42"},
		Sender:    "@alice",
		Text:      "hello",
		MessageID: "7",
		At:        time.Now(),
	})
	select {
	case ev := <-sub:
		if ev.ChatID != 42 || ev.UserID != 42 || ev.MessageID != 7 || ev.Transport != "telegram" {
			t.Fatalf("telegram event ids wrong: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no telegram event broadcast")
	}

	// With a pending ask for user 42, the reply is consumed, not broadcast.
	waiter := make(chan string, 1)
	d.pendingAsk[42] = append(d.pendingAsk[42], waiter)
	d.deliverInbound(transport.Inbound{
		From: transport.Address{Transport: "telegram", ID: "42"},
		Text: "the answer",
		At:   time.Now(),
	})
	select {
	case got := <-waiter:
		if got != "the answer" {
			t.Fatalf("ask got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("pending ask not resolved")
	}
	select {
	case ev := <-sub:
		t.Fatalf("ask reply must not also broadcast, got %+v", ev)
	default:
	}
}
