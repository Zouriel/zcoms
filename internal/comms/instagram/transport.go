// Package instagram is the Instagram DM transport. Instagram has no official API,
// so unlike Telegram (in-process TDLib) and WhatsApp (in-process whatsmeow) this
// transport is a thin client over a Python sidecar (aiograpi/instagrapi behind a
// small REST service — see contrib/instagram-sidecar/). It implements
// comms/transport.Transport so the hub treats Instagram like any other channel:
// sends route by address, and inbound 1:1 DMs — discovered by POLLING the sidecar
// (Instagram has no push) — fan into the shared channel. The account session is
// persisted encrypted on our side (session.go) so restarts rarely re-login. This
// is the most fragile transport by nature of the platform: sessions expire and
// accounts risk automation bans, so status is surfaced honestly and polling is
// deliberately gentle.
package instagram

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"
)

// Compile-time guarantees the transport satisfies the hub contracts. Note: no
// QRProvider — Instagram authenticates with username/password + 2FA/challenge,
// not a QR.
var (
	_ transport.Transport = (*Transport)(nil)
	_ transport.Actor     = (*Transport)(nil)
	_ transport.Reader    = (*Transport)(nil)
)

// Transport is one connected Instagram account.
type Transport struct {
	sc      *sidecarClient
	sess    *sessionStore
	inbound chan<- transport.Inbound
	db      *sql.DB
	ctx     context.Context
	dir     string // ~/.config/zcoms
	dbPath  string // instagram/messages.db
	status  transport.ConnStatus
	cfg     Config

	mu sync.Mutex
}

// New returns an Instagram transport rooted at dir (~/.config/zcoms). The sidecar
// URL comes from ZC_INSTAGRAM_SIDECAR (default http://127.0.0.1:8099).
func New(dir string) *Transport {
	return &Transport{
		dir:    dir,
		dbPath: filepath.Join(dir, "instagram", "messages.db"),
		sc:     newSidecarClient(os.Getenv("ZC_INSTAGRAM_SIDECAR")),
		sess:   newSessionStore(dir),
		status: transport.ConnStatus{State: transport.StateDisconnected, Since: time.Now()},
	}
}

// Name identifies this transport in the hub registry.
func (t *Transport) Name() string { return "instagram" }

// Caps reports that Instagram receives (poll-based) and sends files, but has no
// synchronous blocking-ask — it gets the async auto-reply path.
func (t *Transport) Caps() transport.Caps {
	return transport.Caps{Receive: true, Files: true}
}

// Status returns the current connection state snapshot.
func (t *Transport) Status() transport.ConnStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *Transport) setStatus(state, detail string) {
	t.mu.Lock()
	t.status = transport.ConnStatus{State: state, Detail: detail, Since: time.Now()}
	t.mu.Unlock()
}

func (t *Transport) isConnected() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status.State == transport.StateConnected
}

// Start opens the local store, loads config, restores or establishes the session,
// then polls direct threads until ctx is cancelled.
func (t *Transport) Start(ctx context.Context, inbound chan<- transport.Inbound) error {
	t.mu.Lock()
	t.inbound = inbound
	t.ctx = ctx
	t.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(t.dbPath), 0o700); err != nil {
		t.setStatus(transport.StateError, err.Error())
		return err
	}
	if err := t.openStore(); err != nil {
		t.setStatus(transport.StateError, err.Error())
		return fmt.Errorf("instagram message store: %w", err)
	}
	defer func() {
		t.mu.Lock()
		db := t.db
		t.mu.Unlock()
		if db != nil {
			_ = db.Close()
		}
	}()

	cfg, err := LoadConfig(t.dir)
	if err != nil {
		t.setStatus(transport.StateError, err.Error())
		return err
	}
	t.mu.Lock()
	t.cfg = cfg
	t.mu.Unlock()

	if !cfg.configured() {
		// Inert until the owner seeds instagram.json — mirrors WhatsApp sitting
		// unpaired. No polling, no login attempts.
		t.setStatus(transport.StateDisconnected, "configure ~/.config/zcoms/instagram.json")
	} else {
		t.setStatus(transport.StateConnecting, "")
		// Best-effort silent restore; fall back to a fresh login if the stored
		// session is missing or expired (may park in 2FA/challenge for the console).
		if !t.tryRestore(ctx) {
			t.startLogin(ctx)
		}
	}

	t.pollLoop(ctx, cfg.pollInterval())
	return ctx.Err()
}

// Stop is a no-op: the poll loop exits when Start's ctx is cancelled.
func (t *Transport) Stop() error { return nil }

// tryRestore loads the encrypted session into the sidecar and verifies it. It
// returns true only when the account is live again.
func (t *Transport) tryRestore(ctx context.Context) bool {
	blob, err := t.sess.Load()
	if err != nil || len(blob) == 0 {
		return false
	}
	r, err := t.sc.RestoreSession(ctx, blob)
	if err != nil || r.Status != "ok" {
		return false
	}
	t.setStatus(transport.StateConnected, "")
	t.persistSession(ctx)
	return true
}

// reloadConfig re-reads instagram.json so a login uses the latest credentials.
func (t *Transport) reloadConfig() {
	if cfg, err := LoadConfig(t.dir); err == nil {
		t.mu.Lock()
		t.cfg = cfg
		t.mu.Unlock()
	}
}

// startLogin runs a fresh username/password login and maps the outcome onto the
// action_required sub-states the connectors page understands.
func (t *Transport) startLogin(ctx context.Context) {
	t.mu.Lock()
	cfg := t.cfg
	t.mu.Unlock()
	if !cfg.configured() {
		t.setStatus(transport.StateDisconnected, "configure ~/.config/zcoms/instagram.json")
		return
	}
	r, err := t.sc.Login(ctx, cfg.Username, cfg.Password, cfg.Proxy)
	if err != nil {
		t.setStatus(transport.StateError, err.Error())
		return
	}
	t.applyLoginResult(ctx, r)
}

func (t *Transport) applyLoginResult(ctx context.Context, r loginResult) {
	switch r.Status {
	case "ok":
		t.setStatus(transport.StateConnected, "")
		t.persistSession(ctx)
	case "needs_2fa":
		t.setStatus(transport.StateActionRequired, transport.Needs2FA)
	case "needs_challenge":
		t.setStatus(transport.StateActionRequired, transport.NeedsChallenge)
	case "needs_code":
		t.setStatus(transport.StateActionRequired, transport.NeedsCode)
	default:
		msg := r.Message
		if msg == "" {
			msg = "login failed"
		}
		t.setStatus(transport.StateError, msg)
	}
}

// persistSession dumps the sidecar's current settings and stores them encrypted.
func (t *Transport) persistSession(ctx context.Context) {
	blob, err := t.sc.DumpSession(ctx)
	if err != nil || len(blob) == 0 {
		return
	}
	_ = t.sess.Save(blob)
}

// Action runs a connectors-page command:
//   - "login"        start a username/password login (uses stored credentials)
//   - "reconnect"    restore the session, or log in again if it expired
//   - "code_<value>" submit a pending 2FA / challenge code
//   - "logout"       sign out and drop the stored session
func (t *Transport) Action(name string) error {
	t.mu.Lock()
	ctx := t.ctx
	t.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	switch {
	case name == "login", name == "reconnect", name == "retry", name == "pair":
		// Re-read instagram.json so credentials the owner just entered (via the
		// console) are picked up without a daemon restart.
		t.reloadConfig()
		if t.tryRestore(ctx) {
			return nil
		}
		t.startLogin(ctx)
		return t.actionError()
	case strings.HasPrefix(name, "code_"):
		code := strings.TrimSpace(strings.TrimPrefix(name, "code_"))
		if code == "" {
			return fmt.Errorf("empty code")
		}
		r, err := t.sc.SubmitCode(ctx, code)
		if err != nil {
			t.setStatus(transport.StateError, err.Error())
			return err
		}
		t.applyLoginResult(ctx, r)
		return t.actionError()
	case name == "logout", name == "disconnect":
		_ = t.sc.Logout(ctx)
		_ = t.sess.Clear()
		t.setStatus(transport.StateDisconnected, "")
		return nil
	default:
		return fmt.Errorf("unknown instagram action %q", name)
	}
}

// actionError turns a terminal error state into an error the console surfaces,
// while treating action_required (2FA/challenge pending) and connected as success.
func (t *Transport) actionError() error {
	st := t.Status()
	if st.State == transport.StateError {
		return fmt.Errorf("instagram: %s", st.Detail)
	}
	return nil
}

type pollResult int

const (
	pollOK          pollResult = iota
	pollRateLimited            // Instagram 467/429 soft-block: back off hard
	pollTransient              // other error: back off mildly
)

// pollLoop ticks the receive poll until ctx is cancelled. It only polls while
// connected; when the session drops it surfaces session_expired so the console
// prompts a re-login. On rate-limit/error it backs off exponentially (up to
// maxPollBackoff) so a 467 soft-block from Instagram is never hammered every
// interval — a successful poll resets it to the base cadence.
func (t *Transport) pollLoop(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = 45 * time.Second
	}
	const maxPollBackoff = 15 * time.Minute
	delay := every
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if !t.isConnected() {
			delay = every
			continue
		}
		switch t.pollOnce(ctx) {
		case pollOK:
			delay = every
		case pollRateLimited, pollTransient:
			delay *= 2
			if delay > maxPollBackoff {
				delay = maxPollBackoff
			}
		}
	}
}

// pollOnce fetches recent threads and emits an Inbound for every message it has
// not seen before (deduped by the store's UNIQUE(thread, msg_id) index). It
// returns a pollResult so the loop can pace itself.
func (t *Transport) pollOnce(ctx context.Context) pollResult {
	threads, err := t.sc.Threads(ctx, 20)
	if err != nil {
		e := err.Error()
		switch {
		case strings.Contains(e, "(401)"):
			// The session lapsed; stop polling until the owner re-logs in.
			t.setStatus(transport.StateSessionExpired, "session expired — log in again")
			return pollTransient
		case strings.Contains(e, "(429)") || strings.Contains(e, "467"):
			return pollRateLimited
		default:
			return pollTransient
		}
	}
	for _, th := range threads {
		if th.IsGroup {
			continue // 1:1 only, like the other transports
		}
		for _, m := range th.Messages {
			text := m.Text
			kind := m.ItemType
			if kind == "" {
				kind = "text"
			}
			at := time.Unix(int64(m.Timestamp), 0)
			newRow := t.storeMessage(th.ThreadID, m.ID, th.Username, m.IsFromMe, text, kind, "", at, !m.IsFromMe)
			if !newRow || m.IsFromMe {
				continue
			}
			t.emit(ctx, transport.Inbound{
				From:      transport.Address{Transport: "instagram", ID: th.ThreadID},
				Sender:    th.Username,
				Text:      text,
				Kind:      kind,
				MessageID: m.ID,
				At:        at,
			})
		}
	}
	return pollOK
}

// emit pushes an inbound onto the shared channel without blocking the poll loop
// if a subscriber is slow.
func (t *Transport) emit(ctx context.Context, in transport.Inbound) {
	t.mu.Lock()
	ch := t.inbound
	t.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- in:
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
	}
}

// Send posts a text DM. The address id is either a numeric thread id (a reply to
// an existing conversation) or a "@handle" we resolve to a user id to open a DM.
func (t *Transport) Send(to transport.Address, text string) (transport.MsgRef, error) {
	if !t.isConnected() {
		return transport.MsgRef{}, fmt.Errorf("instagram not connected (%s)", t.Status().State)
	}
	ctx := context.Background()
	threadID, userID, err := t.resolveTarget(ctx, to.ID)
	if err != nil {
		return transport.MsgRef{}, err
	}
	r, err := t.sc.SendText(ctx, threadID, userID, text)
	if err != nil {
		return transport.MsgRef{}, err
	}
	tid := r.ThreadID
	if tid == "" {
		tid = threadID
	}
	t.storeMessage(tid, r.MessageID, "you", true, text, "text", "", time.Now(), false)
	return transport.MsgRef{ID: r.MessageID, ChatID: tid}, nil
}

// SendFile uploads a local file to a DM with an optional caption.
func (t *Transport) SendFile(to transport.Address, path, caption string) (transport.MsgRef, error) {
	if !t.isConnected() {
		return transport.MsgRef{}, fmt.Errorf("instagram not connected (%s)", t.Status().State)
	}
	ctx := context.Background()
	threadID, userID, err := t.resolveTarget(ctx, to.ID)
	if err != nil {
		return transport.MsgRef{}, err
	}
	r, err := t.sc.SendFile(ctx, threadID, userID, path, caption)
	if err != nil {
		return transport.MsgRef{}, err
	}
	tid := r.ThreadID
	if tid == "" {
		tid = threadID
	}
	name := filepath.Base(path)
	t.storeMessage(tid, r.MessageID, "you", true, caption, "media", path, time.Now(), false)
	return transport.MsgRef{ID: r.MessageID, ChatID: tid, Label: name}, nil
}

// resolveTarget maps an address id to (threadID, userID). A "@handle" is resolved
// to a user id via the sidecar; anything else is treated as an existing thread id.
func (t *Transport) resolveTarget(ctx context.Context, id string) (threadID, userID string, err error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", "", fmt.Errorf("empty instagram address")
	}
	if strings.HasPrefix(id, "@") {
		uid, err := t.sc.ResolveUser(ctx, strings.TrimPrefix(id, "@"))
		if err != nil {
			return "", "", fmt.Errorf("resolve instagram @%s: %w", strings.TrimPrefix(id, "@"), err)
		}
		return "", uid, nil
	}
	return id, "", nil
}
