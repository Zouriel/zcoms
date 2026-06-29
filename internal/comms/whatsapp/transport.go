// Package whatsapp is the in-process WhatsApp transport, built on whatsmeow
// (real account via QR multidevice). It implements comms/transport.Transport so
// the hub treats WhatsApp exactly like Telegram: sends route by address, inbound
// 1:1 messages fan into the shared channel. The device session lives in a local
// SQLite store (pure-Go modernc driver) so pairing survives restarts. This
// replaces the Node Baileys sidecar.
package whatsapp

import (
	"context"
	"database/sql"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"

	_ "modernc.org/sqlite" // pure-Go sqlite driver (registered as "sqlite")

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// Compile-time guarantees the transport satisfies the hub contracts.
var (
	_ transport.Transport  = (*Transport)(nil)
	_ transport.QRProvider = (*Transport)(nil)
)

// Transport is one connected WhatsApp account.
type Transport struct {
	dbPath string
	log    waLog.Logger

	mu      sync.Mutex
	client  *whatsmeow.Client
	status  transport.ConnStatus
	qr      string // current QR payload while State==action_required/needs_qr
	me      string // own JID string, for FromSelf
	inbound chan<- transport.Inbound
}

// New returns a WhatsApp transport whose device session persists at dbPath
// (e.g. ~/.config/zcoms/whatsmeow.db). Pass a nil/empty waLog to stay quiet.
func New(dbPath string) *Transport {
	return &Transport{
		dbPath: dbPath,
		log:    waLog.Noop,
		status: transport.ConnStatus{State: transport.StateDisconnected, Since: time.Now()},
	}
}

func (t *Transport) Name() string { return "whatsapp" }

// Caps: WhatsApp receives and sends files but has no synchronous blocking-ask —
// it gets the async auto-reply path.
func (t *Transport) Caps() transport.Caps {
	return transport.Caps{Receive: true, Files: true}
}

func (t *Transport) Status() transport.ConnStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// CurrentQR returns the QR payload to render while pairing (empty otherwise).
// Satisfies transport.QRProvider so the connectors page can show it.
func (t *Transport) CurrentQR() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status.State == transport.StateActionRequired && t.status.Detail == transport.NeedsQR {
		return t.qr
	}
	return ""
}

func (t *Transport) setStatus(state, detail string) {
	t.mu.Lock()
	t.status = transport.ConnStatus{State: state, Detail: detail, Since: time.Now()}
	if state != transport.StateActionRequired {
		t.qr = ""
	}
	t.mu.Unlock()
}

func (t *Transport) setQR(code string) {
	t.mu.Lock()
	t.status = transport.ConnStatus{State: transport.StateActionRequired, Detail: transport.NeedsQR, Since: time.Now()}
	t.qr = code
	t.mu.Unlock()
}

// Start opens the device store, builds the client, and connects — driving the QR
// pairing flow when no session exists yet. It blocks until ctx is cancelled.
func (t *Transport) Start(ctx context.Context, inbound chan<- transport.Inbound) error {
	t.inbound = inbound
	t.setStatus(transport.StateConnecting, "")

	if err := os.MkdirAll(filepath.Dir(t.dbPath), 0o700); err != nil {
		t.setStatus(transport.StateError, err.Error())
		return err
	}
	// modernc needs the busy-timeout/foreign-keys pragmas set in the DSN.
	dsn := "file:" + t.dbPath + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.setStatus(transport.StateError, err.Error())
		return err
	}
	defer db.Close()

	container := sqlstore.NewWithDB(db, "sqlite3", t.log)
	if err := container.Upgrade(ctx); err != nil {
		t.setStatus(transport.StateError, err.Error())
		return fmt.Errorf("whatsmeow store upgrade: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		t.setStatus(transport.StateError, err.Error())
		return fmt.Errorf("whatsmeow device: %w", err)
	}

	client := whatsmeow.NewClient(device, t.log)
	t.mu.Lock()
	t.client = client
	t.mu.Unlock()
	client.AddEventHandler(t.handleEvent)

	if client.Store.ID == nil {
		// Not paired yet: surface a QR for the connectors page to render.
		qrChan, err := client.GetQRChannel(ctx)
		if err != nil {
			t.setStatus(transport.StateError, err.Error())
			return fmt.Errorf("whatsmeow qr channel: %w", err)
		}
		if err := client.Connect(); err != nil {
			t.setStatus(transport.StateError, err.Error())
			return fmt.Errorf("whatsmeow connect: %w", err)
		}
		go t.consumeQR(qrChan)
	} else {
		t.mu.Lock()
		t.me = client.Store.ID.String()
		t.mu.Unlock()
		if err := client.Connect(); err != nil {
			t.setStatus(transport.StateError, err.Error())
			return fmt.Errorf("whatsmeow connect: %w", err)
		}
	}

	<-ctx.Done()
	client.Disconnect()
	return ctx.Err()
}

func (t *Transport) Stop() error {
	t.mu.Lock()
	c := t.client
	t.mu.Unlock()
	if c != nil {
		c.Disconnect()
	}
	return nil
}

// consumeQR turns whatsmeow's QR event stream into status updates: each "code"
// item is a fresh QR payload; success/timeout end the flow.
func (t *Transport) consumeQR(qrChan <-chan whatsmeow.QRChannelItem) {
	for item := range qrChan {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			t.setQR(item.Code)
		case "success":
			t.setStatus(transport.StateConnected, "")
		case "timeout":
			t.setStatus(transport.StateError, "qr timed out — retry pairing")
		default:
			if item.Error != nil {
				t.setStatus(transport.StateError, item.Error.Error())
			}
		}
	}
}

// handleEvent reacts to whatsmeow events: connection state and inbound messages.
func (t *Transport) handleEvent(evt any) {
	switch e := evt.(type) {
	case *events.Connected:
		t.mu.Lock()
		if t.client != nil && t.client.Store.ID != nil {
			t.me = t.client.Store.ID.String()
		}
		t.mu.Unlock()
		t.setStatus(transport.StateConnected, "")
	case *events.Disconnected:
		// whatsmeow auto-reconnects; reflect the transient state.
		if t.Status().State == transport.StateConnected {
			t.setStatus(transport.StateConnecting, "")
		}
	case *events.LoggedOut:
		t.setStatus(transport.StateSessionExpired, "")
	case *events.PairSuccess:
		t.mu.Lock()
		t.me = e.ID.String()
		t.mu.Unlock()
		t.setStatus(transport.StateConnected, "")
	case *events.Message:
		t.onMessage(e)
	}
}

// onMessage converts an inbound 1:1 WhatsApp message into a transport.Inbound.
// Groups, broadcasts and newsletters are ignored — comms handles direct chats
// only, mirroring the Telegram transport.
func (t *Transport) onMessage(e *events.Message) {
	if e.Info.IsGroup || e.Info.Chat.Server != types.DefaultUserServer {
		return
	}
	text := messageText(e.Message)
	if text == "" {
		text = "[" + e.Info.Type + "]"
	}
	t.mu.Lock()
	me := t.me
	t.mu.Unlock()

	in := transport.Inbound{
		From:      transport.Address{Transport: "whatsapp", ID: e.Info.Chat.String()},
		FromSelf:  e.Info.IsFromMe || (me != "" && e.Info.Sender.String() == me),
		Sender:    e.Info.PushName,
		Text:      text,
		Kind:      e.Info.Type,
		MessageID: e.Info.ID,
		At:        e.Info.Timestamp,
	}
	if in.Sender == "" {
		in.Sender = e.Info.Sender.User
	}
	if t.inbound != nil {
		select {
		case t.inbound <- in:
		case <-time.After(5 * time.Second):
		}
	}
}

// messageText pulls the human text out of the common message shapes.
func messageText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if c := m.GetConversation(); c != "" {
		return c
	}
	if ext := m.GetExtendedTextMessage(); ext != nil {
		return ext.GetText()
	}
	if img := m.GetImageMessage(); img != nil {
		return img.GetCaption()
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return vid.GetCaption()
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		return doc.GetCaption()
	}
	return ""
}

// Send posts a text message to a WhatsApp chat (the address is a JID string).
func (t *Transport) Send(to transport.Address, text string) (transport.MsgRef, error) {
	c, err := t.connected()
	if err != nil {
		return transport.MsgRef{}, err
	}
	jid, err := types.ParseJID(to.ID)
	if err != nil {
		return transport.MsgRef{}, fmt.Errorf("bad whatsapp jid %q: %w", to.ID, err)
	}
	resp, err := c.SendMessage(context.Background(), jid, &waE2E.Message{Conversation: proto.String(text)})
	if err != nil {
		return transport.MsgRef{}, err
	}
	return transport.MsgRef{ID: resp.ID, ChatID: jid.String()}, nil
}

// SendFile uploads a local file and sends it as a document with an optional
// caption (documents carry any file type reliably).
func (t *Transport) SendFile(to transport.Address, path, caption string) (transport.MsgRef, error) {
	c, err := t.connected()
	if err != nil {
		return transport.MsgRef{}, err
	}
	jid, err := types.ParseJID(to.ID)
	if err != nil {
		return transport.MsgRef{}, fmt.Errorf("bad whatsapp jid %q: %w", to.ID, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return transport.MsgRef{}, err
	}
	ctx := context.Background()
	up, err := c.Upload(ctx, data, whatsmeow.MediaDocument)
	if err != nil {
		return transport.MsgRef{}, fmt.Errorf("whatsapp upload: %w", err)
	}
	name := filepath.Base(path)
	mimeType := mime.TypeByExtension(filepath.Ext(path))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	doc := &waE2E.DocumentMessage{
		URL:           proto.String(up.URL),
		DirectPath:    proto.String(up.DirectPath),
		MediaKey:      up.MediaKey,
		FileEncSHA256: up.FileEncSHA256,
		FileSHA256:    up.FileSHA256,
		FileLength:    proto.Uint64(up.FileLength),
		Mimetype:      proto.String(mimeType),
		FileName:      proto.String(name),
		Title:         proto.String(name),
	}
	if strings.TrimSpace(caption) != "" {
		doc.Caption = proto.String(caption)
	}
	resp, err := c.SendMessage(ctx, jid, &waE2E.Message{DocumentMessage: doc})
	if err != nil {
		return transport.MsgRef{}, err
	}
	return transport.MsgRef{ID: resp.ID, ChatID: jid.String(), Label: name}, nil
}

// connected returns the live client or a descriptive error so a send before
// pairing fails clearly instead of panicking.
func (t *Transport) connected() (*whatsmeow.Client, error) {
	t.mu.Lock()
	c, st := t.client, t.status
	t.mu.Unlock()
	if c == nil || !c.IsConnected() {
		return nil, fmt.Errorf("whatsapp not connected (%s)", st.State)
	}
	if c.Store.ID == nil {
		return nil, fmt.Errorf("whatsapp not paired — scan the QR on the connectors page")
	}
	return c, nil
}
