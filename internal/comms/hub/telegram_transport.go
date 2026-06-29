package hub

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/comms/transport"
)

// telegramTransport adapts the daemon's in-process TDLib session to the
// transport.Transport contract. It holds a back-reference to the daemon so it
// can reuse the session handles (tdjson/clientID) and the media/sender helpers
// that only the session owner may call. Send resolves @usernames itself; Start
// runs the TDLib receive loop and pushes every inbound 1:1 message onto the
// shared channel.
type telegramTransport struct {
	d *daemon

	mu    sync.Mutex
	state transport.ConnStatus
}

func newTelegramTransport(d *daemon) *telegramTransport {
	return &telegramTransport{
		d:     d,
		state: transport.ConnStatus{State: transport.StateConnecting, Since: time.Now()},
	}
}

func (t *telegramTransport) Name() string { return "telegram" }

// Caps: Telegram is the reference transport — full receive, the synchronous
// blocking-ask mode, and file send/receive.
func (t *telegramTransport) Caps() transport.Caps {
	return transport.Caps{Receive: true, BlockingAsk: true, Files: true}
}

func (t *telegramTransport) Status() transport.ConnStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state
}

func (t *telegramTransport) setState(state, detail string) {
	t.mu.Lock()
	t.state = transport.ConnStatus{State: state, Detail: detail, Since: time.Now()}
	t.mu.Unlock()
}

// Send resolves the address (an @username or numeric chat id) and posts text,
// splitting anything over Telegram's length limit. The returned MsgRef carries
// the last part's message id and the resolved chat id.
func (t *telegramTransport) Send(to transport.Address, text string) (transport.MsgRef, error) {
	chatID, _, err := t.d.resolveChat(to.ID)
	if err != nil {
		return transport.MsgRef{}, err
	}
	var last int64
	for _, part := range chunk(text, telegramMaxLen) {
		id, err := telegram.SendTextMessage(t.d.tdjson, t.d.clientID, chatID, part)
		if err != nil {
			return transport.MsgRef{}, err
		}
		last = id
	}
	return transport.MsgRef{
		ID:     strconv.FormatInt(last, 10),
		ChatID: strconv.FormatInt(chatID, 10),
	}, nil
}

// SendFile resolves the address and uploads a local file. The upload completes
// in the background (the receive loop consumes the completion update), so this
// returns once the send is accepted, with the human label for the attachment.
func (t *telegramTransport) SendFile(to transport.Address, path, caption string) (transport.MsgRef, error) {
	chatID, _, err := t.d.resolveChat(to.ID)
	if err != nil {
		return transport.MsgRef{}, err
	}
	_, label, err := telegram.SendLocalFileMessage(t.d.tdjson, t.d.clientID, chatID, path, caption)
	if err != nil {
		return transport.MsgRef{}, err
	}
	return transport.MsgRef{ChatID: strconv.FormatInt(chatID, 10), Label: label}, nil
}

// Start records the connected account's own id (for FromSelf) and then runs the
// TDLib receive loop, converting each incoming 1:1 message into a
// transport.Inbound on the shared channel until ctx is cancelled. A remote
// logout flips the status to session_expired and marks config unauthorized.
func (t *telegramTransport) Start(ctx context.Context, inbound chan<- transport.Inbound) error {
	if u, err := telegram.FetchCurrentUser(t.d.tdjson, t.d.clientID); err == nil {
		t.d.mu.Lock()
		t.d.me = u.ID
		t.d.mu.Unlock()
	}
	t.setState(transport.StateConnected, "")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		updateJSON, err := telegram.ReceiveUpdates(t.d.tdjson)
		if err != nil || updateJSON == "" {
			continue
		}
		t.dispatchUpdate(updateJSON, inbound)
	}
}

func (t *telegramTransport) Stop() error { return nil }

// dispatchUpdate handles one TDLib update under a recover so a panic parsing
// untrusted JSON can never crash the receive loop. Incoming 1:1 messages are
// pushed onto the inbound channel; the daemon's consumer resolves any pending
// `ask` or broadcasts to subscribers.
func (t *telegramTransport) dispatchUpdate(updateJSON string, inbound chan<- transport.Inbound) {
	defer func() {
		if r := recover(); r != nil {
			t.d.logf("recovered from panic handling update: %v", r)
		}
	}()

	// A remote logout: keep config.json honest and surface session_expired.
	if state, ok := telegram.ParseUpdateAuthorizationState(updateJSON); ok {
		if state == telegram.AuthStateLoggingOut || state == telegram.AuthStateClosed {
			t.d.logf("⚠️ Telegram session %s — marking config unauthorized; needs `zc tg login` (stop the daemon first).", state)
			markConfigUnauthorized()
			t.setState(transport.StateSessionExpired, "")
		}
		return
	}

	u, ok := telegram.ParseUpdateNewMessage(updateJSON)
	if !ok || u.Message.IsOutgoing {
		return
	}
	if u.Message.SenderID.Type != "messageSenderUser" {
		return
	}
	// Only 1:1 private chats: a private chat's id equals the peer's user id;
	// anything else is a group/supergroup/channel the comms pipe stays silent in.
	if u.Message.ChatID != u.Message.SenderID.UserID {
		return
	}

	msg := u.Message
	t.d.mu.Lock()
	me := t.d.me
	t.d.mu.Unlock()

	in := transport.Inbound{
		From:      transport.Address{Transport: "telegram", ID: strconv.FormatInt(msg.ChatID, 10)},
		FromSelf:  me != 0 && msg.SenderID.UserID == me,
		Sender:    t.d.senderName(msg.SenderID.UserID),
		Text:      replyText(msg.Content),
		Kind:      msg.Content.Type,
		MessageID: strconv.FormatInt(msg.ID, 10),
		At:        time.Unix(msg.Date, 0),
	}
	if msg.Content.Type != "messageText" {
		if f := t.d.downloadMessageMedia(msg); f != "" {
			in.Files = []string{f}
		}
	}

	select {
	case inbound <- in:
	case <-time.After(5 * time.Second):
		t.d.logf("inbound channel full — dropped a telegram message from %s", in.Sender)
	}
}
