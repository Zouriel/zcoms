// Package hub is the comms daemon: it owns the single TDLib Telegram session
// behind a non-shareable lock and serves it to every upper tier over the IPC
// socket (client.DefaultSocketPath). It is a dumb pipe — it knows nothing about
// AI, allow-lists, personas, or claims. Inbound 1:1 messages are pushed to
// whatever subscribed (the agent tier decides what to do with them); the agent
// and modules act through the IPC ops (send/ask/read/unread/mark_read/resolve/
// contacts) and their own command sockets.
package hub

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/config"
)

const telegramMaxLen = 4000

type daemon struct {
	tdjson   *telegram.TDJSON
	clientID int32

	contacts *contacts.Store // comms.db — the contacts directory

	mu         sync.Mutex
	pendingAsk map[int64][]chan string // user id -> queued `zc tg ask` waiters
	nameCache  map[int64]string        // user id -> display name

	// subscribers receive pushed incoming-message events by role. The daemon
	// never blocks on a slow subscriber (pushEvent drops when the buffer fills).
	subMu       sync.Mutex
	subscribers map[string][]chan client.Event
}

// RunDaemon owns the Telegram session, serves the IPC socket, and pumps incoming
// 1:1 messages out to subscribers until interrupted. It carries no AI config —
// allow-listing, routing, and replies all live in the agent tier above.
func RunDaemon(tdjson *telegram.TDJSON, clientID int32, store *contacts.Store) error {
	d := &daemon{
		tdjson:      tdjson,
		clientID:    clientID,
		contacts:    store,
		pendingAsk:  map[int64][]chan string{},
		nameCache:   map[int64]string{},
		subscribers: map[string][]chan client.Event{},
	}

	if err := d.serveIPC(); err != nil {
		fmt.Printf("  ! IPC socket unavailable (zc tg send/ask won't route through daemon): %v\n", err)
	}

	fmt.Printf("comms daemon running (protocol v%d). Listening…\n", client.ProtocolVersion)
	fmt.Println("⚠️  SECURITY: the agent tier can drive an AI agent on this machine for allow-listed")
	fmt.Println("    users. Roles limit WRITES, not reads. Keep the allowlist tiny and enable 2FA.")

	for {
		updateJSON, err := telegram.ReceiveUpdates(tdjson)
		if err != nil || updateJSON == "" {
			continue
		}
		d.dispatchUpdate(updateJSON)
	}
}

// dispatchUpdate handles one incoming TDLib update under a recover so a panic
// parsing untrusted JSON can never crash the receive loop.
func (d *daemon) dispatchUpdate(updateJSON string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[comms] recovered from panic handling update: %v\n", r)
		}
	}()

	// If Telegram logs this session out remotely, keep config.json honest.
	if state, ok := telegram.ParseUpdateAuthorizationState(updateJSON); ok {
		if state == telegram.AuthStateLoggingOut || state == telegram.AuthStateClosed {
			fmt.Printf("[comms] ⚠️ Telegram session %s — marking config unauthorized; needs `zc tg login` (stop the daemon first).\n", state)
			markConfigUnauthorized()
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
	// Only ever act on 1:1 private chats. In TDLib a private chat's id equals the
	// peer's user id; anything else is a group/supergroup/channel, which the
	// comms pipe must stay completely silent in.
	if u.Message.ChatID != u.Message.SenderID.UserID {
		return
	}

	// A reply from anyone with an outstanding `zc tg ask` resolves it first.
	if d.resolvePendingAsk(u.Message.SenderID.UserID, replyText(u.Message.Content)) {
		return
	}

	// Otherwise push it to the subscribed agent tier, which owns all policy
	// (allow-list, routing, auto-reply, triage, errands). Comms does not decide.
	d.pushIncoming(u.Message)
}

// pushIncoming builds an Event for an incoming 1:1 message (downloading any
// attachment first, since only the daemon owns the session) and fans it out to
// every subscriber across roles.
func (d *daemon) pushIncoming(msg telegram.Message) {
	ev := client.Event{
		Event:     "message",
		ChatID:    msg.ChatID,
		UserID:    msg.SenderID.UserID,
		Sender:    d.senderName(msg.SenderID.UserID),
		Text:      replyText(msg.Content),
		Kind:      msg.Content.Type,
		MessageID: msg.ID,
		Date:      msg.Date,
	}
	if msg.Content.Type != "messageText" {
		ev.File = d.downloadMessageMedia(msg)
	}
	d.broadcast(ev)
}

// senderName resolves the value stamped into Event.Sender. It is the user's
// "@username" handle when they have one — the stable key the agent tier matches
// against the allow-list, claims, and contacts — falling back to their display
// name, then "user:<id>", only when there is no public username.
func (d *daemon) senderName(userID int64) string {
	d.mu.Lock()
	if cached, ok := d.nameCache[userID]; ok {
		d.mu.Unlock()
		return cached
	}
	d.mu.Unlock()

	name := ""
	if handle, err := telegram.FetchUserHandle(d.tdjson, d.clientID, userID); err == nil && handle != "" {
		name = handle
	} else if dn, err := telegram.FetchUserDisplayName(d.tdjson, d.clientID, userID); err == nil && dn != "" {
		name = dn
	}
	if name == "" {
		name = fmt.Sprintf("user:%d", userID)
	}
	d.mu.Lock()
	d.nameCache[userID] = name
	d.mu.Unlock()
	return name
}

// resolveChat turns "@username" or a numeric id into a chat id (and user id).
func (d *daemon) resolveChat(to string) (chatID, userID int64, err error) {
	to = strings.TrimSpace(to)
	if id, e := strconv.ParseInt(to, 10, 64); e == nil {
		return id, id, nil // private chat id == user id in TDLib
	}
	uid, e := telegram.ResolveUserIdentifierByUsername(d.tdjson, d.clientID, to)
	if e != nil {
		return 0, 0, e
	}
	cid, e := telegram.CreatePrivateChat(d.tdjson, d.clientID, uid)
	if e != nil {
		cid = uid
	}
	return cid, uid, nil
}

// resolvePendingAsk delivers text to the oldest outstanding `zc tg ask` for
// userID, returning true if one was waiting.
func (d *daemon) resolvePendingAsk(userID int64, text string) bool {
	d.mu.Lock()
	queue := d.pendingAsk[userID]
	if len(queue) == 0 {
		d.mu.Unlock()
		return false
	}
	ch := queue[0]
	d.pendingAsk[userID] = queue[1:]
	d.mu.Unlock()
	ch <- text
	return true
}

func (d *daemon) removePending(userID int64, ch chan string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	queue := d.pendingAsk[userID]
	for i, c := range queue {
		if c == ch {
			d.pendingAsk[userID] = append(queue[:i], queue[i+1:]...)
			return
		}
	}
}

// send posts text to a chat, splitting anything over Telegram's length limit.
func (d *daemon) send(chatID int64, text string) {
	for _, part := range chunk(text, telegramMaxLen) {
		if _, err := telegram.SendTextMessage(d.tdjson, d.clientID, chatID, part); err != nil {
			fmt.Printf("  ! send failed: %v\n", err)
			return
		}
	}
}

func chunk(s string, max int) []string {
	if len(s) <= max {
		return []string{s}
	}
	var out []string
	for len(s) > max {
		cut := strings.LastIndex(s[:max], "\n")
		if cut <= 0 {
			cut = max
		}
		out = append(out, s[:cut])
		s = s[cut:]
	}
	if strings.TrimSpace(s) != "" {
		out = append(out, s)
	}
	return out
}

// replyText extracts a user's reply (text, else caption, else a [type] tag).
func replyText(c telegram.Content) string {
	if c.Type == "messageText" {
		return c.Text.Text
	}
	if t := c.CaptionOrText(); t != "" {
		return t
	}
	return "[" + c.Type + "]"
}

// markConfigUnauthorized flips config.json's auth_state to unauthorized (e.g.
// after a remote logout) while preserving the other fields. Best-effort.
func markConfigUnauthorized() {
	cfg, path, err := config.LoadOrCreate()
	if err != nil {
		return
	}
	cfg.AuthState = config.AuthStateUnauthorized
	_ = config.Save(cfg, path)
}

func humanAgo(t time.Time) string {
	dur := time.Since(t)
	switch {
	case dur < time.Minute:
		return "just now"
	case dur < time.Hour:
		return fmt.Sprintf("%dm ago", int(dur.Minutes()))
	case dur < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(dur.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(dur.Hours()/24))
	}
}
