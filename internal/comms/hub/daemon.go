// Package hub is the comms daemon: it owns the single TDLib Telegram session
// behind a non-shareable lock and serves it to every upper tier over the IPC
// socket (client.DefaultSocketPath). It is a dumb pipe — it knows nothing about
// AI, allow-lists, personas, or claims. Inbound 1:1 messages are pushed to
// whatever subscribed (the agent tier decides what to do with them); the agent
// and modules act through the IPC ops (send/ask/read/unread/mark_read/resolve/
// contacts) and their own command sockets.
package hub

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"
	"github.com/Zouriel/zcoms/internal/comms/instagram"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/comms/transport"
	"github.com/Zouriel/zcoms/internal/comms/whatsapp"
	"github.com/Zouriel/zcoms/internal/config"
)

const telegramMaxLen = 4000

type daemon struct {
	tdjson   *telegram.TDJSON
	clientID int32

	contacts *contacts.Store // comms.db — the contacts directory

	// registry maps transport name → connector. Sends route by name; inbound
	// from every transport fans into the shared `inbound` channel, which the
	// consumer turns into client.Events and broadcasts to subscribers.
	registry map[string]transport.Transport
	inbound  chan transport.Inbound

	mu         sync.Mutex
	pendingAsk map[int64][]chan string // user id -> queued `zc tg ask` waiters
	nameCache  map[int64]string        // user id -> display name
	me         int64                   // telegram getMe id (FromSelf); guarded by mu

	// subscribers receive pushed incoming-message events by role. The daemon
	// never blocks on a slow subscriber (pushEvent drops when the buffer fills).
	subMu       sync.Mutex
	subscribers map[string][]chan client.Event
}

func (d *daemon) logf(format string, a ...any) { fmt.Printf("[comms] "+format+"\n", a...) }

// transportNames returns the registry keys, sorted, for stable iteration.
func (d *daemon) transportNames() []string {
	names := make([]string, 0, len(d.registry))
	for name := range d.registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// RunDaemon owns the Telegram session, serves the IPC socket, and pumps incoming
// 1:1 messages out to subscribers until interrupted. It carries no AI config —
// allow-listing, routing, and replies all live in the agent tier above.
func RunDaemon(tdjson *telegram.TDJSON, clientID int32, store *contacts.Store) error {
	d := &daemon{
		tdjson:      tdjson,
		clientID:    clientID,
		contacts:    store,
		registry:    map[string]transport.Transport{},
		inbound:     make(chan transport.Inbound, 256),
		pendingAsk:  map[int64][]chan string{},
		nameCache:   map[int64]string{},
		subscribers: map[string][]chan client.Event{},
	}

	// Telegram is the in-process reference transport. WhatsApp/Instagram register
	// here too once their connectors land (Phases B/C); the rest of the daemon is
	// already transport-agnostic.
	d.registry["telegram"] = newTelegramTransport(d)

	// WhatsApp (whatsmeow, in-process). Inert until paired: it sits in
	// action_required/needs_qr and surfaces the QR on the connectors page.
	if dir, err := client.DefaultAppDir(); err == nil {
		d.registry["whatsapp"] = whatsapp.New(filepath.Join(dir, "whatsmeow.db"))
		// Instagram (Python sidecar over HTTP). Inert until instagram.json is
		// seeded; then it restores/logs in and polls DMs on an interval.
		d.registry["instagram"] = instagram.New(dir)
	} else {
		d.logf("whatsapp/instagram transports disabled: %v", err)
	}

	if err := d.serveIPC(); err != nil {
		fmt.Printf("  ! IPC socket unavailable (zc tg send/ask won't route through daemon): %v\n", err)
	}

	fmt.Printf("comms daemon running (protocol v%d). Listening…\n", client.ProtocolVersion)
	fmt.Println("⚠️  SECURITY: the agent tier can drive an AI agent on this machine for allow-listed")
	fmt.Println("    users. Roles limit WRITES, not reads. Keep the allowlist tiny and enable 2FA.")

	ctx := context.Background()

	// One consumer turns the fan-in channel into broadcast events for everyone.
	go d.consumeInbound()

	// Start every registered transport's receive loop. Telegram's loop blocks
	// the session, so it owns the foreground; others (if any) run in goroutines.
	for _, name := range d.transportNames() {
		t := d.registry[name]
		go func(name string, t transport.Transport) {
			if err := t.Start(ctx, d.inbound); err != nil && err != context.Canceled {
				d.logf("transport %s stopped: %v", name, err)
			}
		}(name, t)
	}

	select {} // run until the process is signalled
}

// consumeInbound drains the shared inbound channel, delivering each message.
func (d *daemon) consumeInbound() {
	for in := range d.inbound {
		d.deliverInbound(in)
	}
}

// deliverInbound turns a transport.Inbound into a client.Event and fans it out.
// For Telegram it first satisfies any outstanding `zc tg ask` (the blocking-ask
// path) before broadcasting to the subscribed agent tier, which owns all policy
// (allow-list, routing, auto-reply, triage). Comms itself decides nothing.
func (d *daemon) deliverInbound(in transport.Inbound) {
	if in.From.Transport == "telegram" {
		if uid, err := strconv.ParseInt(in.From.ID, 10, 64); err == nil {
			if d.resolvePendingAsk(uid, in.Text) {
				return
			}
		}
	}

	ev := client.Event{
		Event:     "message",
		Transport: in.From.Transport,
		Address:   in.From.ID,
		Sender:    in.Sender,
		Text:      in.Text,
		Kind:      in.Kind,
		MessageID: parseIntOrZero(in.MessageID),
		MsgRef:    in.MessageID,
		Date:      in.At.Unix(),
		FromSelf:  in.FromSelf,
	}
	if len(in.Files) > 0 {
		ev.File = in.Files[0]
	}
	// Telegram's numeric ids stay populated for back-compat with existing
	// subscribers (the bridge reads ChatID/UserID); a 1:1 chat id == the user id.
	if in.From.Transport == "telegram" {
		if cid, err := strconv.ParseInt(in.From.ID, 10, 64); err == nil {
			ev.ChatID = cid
			ev.UserID = cid
		}
	}
	d.broadcast(ev)
}

func parseIntOrZero(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// collectUnread merges Telegram unread (read live from TDLib) with the unread of
// every transport that keeps a readable store (WhatsApp, …), tagging each item
// with its transport so triage can group and mark-read correctly.
func (d *daemon) collectUnread() []client.UnreadItem {
	items := d.collectUnreadTG()
	for _, name := range d.transportNames() {
		if name == "telegram" {
			continue
		}
		rdr, ok := d.registry[name].(transport.Reader)
		if !ok {
			continue
		}
		ins, err := rdr.Unread()
		if err != nil {
			d.logf("unread(%s): %v", name, err)
			continue
		}
		for _, in := range ins {
			it := client.UnreadItem{
				Sender:    in.Sender,
				Text:      in.Text,
				When:      in.At.Unix(),
				Transport: in.From.Transport,
				Address:   in.From.ID,
				MsgRef:    in.MessageID,
			}
			if len(in.Files) > 0 {
				it.File = in.Files[0]
			}
			items = append(items, it)
		}
	}
	return items
}

// connectors snapshots every registered transport's status for the `connectors`
// op (the console's Connectors page renders one card per entry).
func (d *daemon) connectors() []client.Connector {
	var out []client.Connector
	for _, name := range d.transportNames() {
		t := d.registry[name]
		st := t.Status()
		caps := t.Caps()
		c := client.Connector{
			Transport: name,
			State:     st.State,
			Detail:    st.Detail,
			Caps: client.Caps{
				Receive:     caps.Receive,
				BlockingAsk: caps.BlockingAsk,
				Files:       caps.Files,
				Presence:    caps.Presence,
			},
		}
		if !st.Since.IsZero() {
			c.Since = st.Since.Unix()
		}
		if qp, ok := t.(transport.QRProvider); ok {
			c.QR = qp.CurrentQR()
		}
		out = append(out, c)
	}
	return out
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

// resolveChat turns a numeric id, an "@username", or a contact's name into a
// chat id (and user id).
//
// The contacts directory is consulted for a bare name because that is the
// spelling callers naturally have: the directory is what list_contacts hands an
// AI, and "Shanna" is not a Telegram username, so passing it straight through
// asks Telegram to look up a public account and comes back USERNAME_INVALID.
// The round trip has to close at the layer that owns the directory, or every
// caller re-implements it and only some get it right.
func (d *daemon) resolveChat(to string) (chatID, userID int64, err error) {
	to = strings.TrimSpace(to)
	if id, ok := chatIDOf(to); ok {
		return id, id, nil // private chat id == user id in TDLib
	}

	// An explicit "@handle" means Telegram, so it skips the directory. A bare
	// word is looked up as a contact first: the owner's own directory outranks
	// whoever happens to hold that public username.
	handle := to
	if !strings.HasPrefix(to, "@") {
		if resolved, name, e := d.telegramHandleFor(to); e != nil {
			return 0, 0, e
		} else if resolved != "" {
			handle = resolved
			defer func() {
				if err != nil {
					err = fmt.Errorf("couldn't reach %s on Telegram (%s): %w", name, resolved, err)
				}
			}()
		}
	}

	uid, e := telegram.ResolveUserIdentifierByUsername(d.tdjson, d.clientID, handle)
	if e != nil {
		return 0, 0, e
	}
	cid, e := telegram.CreatePrivateChat(d.tdjson, d.clientID, uid)
	if e != nil {
		cid = uid
	}
	return cid, uid, nil
}

// telegramHandleFor looks a name up in the contacts directory and returns the
// Telegram address to use. An empty handle with no error means "not a contact",
// so the caller falls back to treating the name as a username.
func (d *daemon) telegramHandleFor(name string) (handle, contact string, err error) {
	if d.contacts == nil {
		return "", "", nil
	}

	matches, e := d.contacts.Resolve(name)
	if e != nil || len(matches) == 0 {
		return "", "", nil // a lookup failure is not a reason to refuse; try it as a username
	}

	// Ambiguity is the caller's to settle. Picking one silently sends a private
	// message to the wrong person, which is not a mistake a retry can undo.
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name)
		}
		return "", "", fmt.Errorf("%q matches several contacts (%s): say which one",
			name, strings.Join(names, ", "))
	}

	c := matches[0]
	addr := strings.TrimSpace(c.Address("telegram"))
	if addr == "" {
		return "", "", fmt.Errorf("%s has no Telegram address in your contacts", c.Name)
	}

	// Address falls back to the phone number, which Telegram cannot look up the
	// way a username can. Saying so beats handing back a Telegram error code.
	if _, ok := chatIDOf(addr); !ok && !strings.HasPrefix(addr, "@") {
		return "", "", fmt.Errorf(
			"%s is saved with a phone number (%s) and no Telegram handle, which I cannot look up: add their @username",
			c.Name, addr)
	}

	return addr, c.Name, nil
}

// chatIDOf reports whether s is a Telegram chat id.
//
// It is stricter than ParseInt, which accepts a leading sign: "+9607988692" is
// a phone number, and reading it as a chat id would address a chat nobody meant.
// A leading "-" stays valid, because supergroup ids are negative.
func chatIDOf(s string) (int64, bool) {
	if strings.HasPrefix(s, "+") {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil
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
