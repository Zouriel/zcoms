package agent

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"tg/internal/tdlib"
)

const telegramMaxLen = 4000

// userState tracks one allow-listed user's place in the bridge.
type userState struct {
	username string
	entry    AllowEntry
	chatID   int64

	location      string // active location name ("" = none picked)
	locationPath  string
	effectiveRole Role    // user role capped by the location's max_role
	backend       Backend // resolved agent backend ("" = none installed)
	sessionID     string  // active agent session ("" = will start fresh)

	pendingKind     string    // "location" | "session" | ""
	pendingLoc      []string  // location names awaiting numeric pick
	pendingSess     []Session // sessions awaiting numeric pick
	busy            bool      // an agent run is in flight
	awaitingConfirm bool      // a plan is waiting for the user's yes/no (confirm role)
}

type daemon struct {
	tdjson    *tdlib.TDJSON
	clientID  int32
	locations Locations
	settings  Settings
	agents    AgentConfig

	mainChatID    int64   // where triage digests are sent
	mainUserID    int64   // the owner — never auto-replied to or triaged
	triageBackend Backend // resolved backend for the triage task

	mu            sync.Mutex
	byUser        map[int64]*userState    // resolved user id -> state
	pendingAsk    map[int64][]chan string // user id -> queued `tg ask` waiters
	inbox         []inboxMessage          // stranger messages awaiting triage
	lastAutoReply map[int64]time.Time     // user id -> last auto-reply time
	nameCache     map[int64]string        // user id -> display name
}

// RunDaemon resolves the allow-list, greets each member, then services incoming
// messages until interrupted.
func RunDaemon(tdjson *tdlib.TDJSON, clientID int32, locations Locations, allow Allowlist, settings Settings, agents AgentConfig) error {
	d := &daemon{
		tdjson:        tdjson,
		clientID:      clientID,
		locations:     locations,
		settings:      settings,
		agents:        agents,
		triageBackend: agents.For("triage", ""),
		byUser:        map[int64]*userState{},
		pendingAsk:    map[int64][]chan string{},
		lastAutoReply: map[int64]time.Time{},
		nameCache:     map[int64]string{},
	}
	d.inbox = loadInbox() // restore any stranger messages buffered before a restart

	if err := d.serveIPC(); err != nil {
		fmt.Printf("  ! IPC socket unavailable (tg send/ask won't route through daemon): %v\n", err)
	}

	// Resolve the main user (for triage digests).
	if settings.MainUser != "" && settings.MainUser != "@your_username" {
		if uid, err := tdlib.ResolveUserIdentifierByUsername(tdjson, clientID, settings.MainUser); err == nil {
			d.mainUserID = uid
			if cid, err := tdlib.CreatePrivateChat(tdjson, clientID, uid); err == nil {
				d.mainChatID = cid
			} else {
				d.mainChatID = uid
			}
		}
	}

	resolved := 0
	for username, entry := range allow {
		userID, err := tdlib.ResolveUserIdentifierByUsername(tdjson, clientID, username)
		if err != nil {
			fmt.Printf("  ! could not resolve %s: %v (skipping)\n", username, err)
			continue
		}
		chatID, err := tdlib.CreatePrivateChat(tdjson, clientID, userID)
		if err != nil {
			chatID = userID // private chat id == user id in TDLib
		}
		backend := agents.For("", entry.Agent)
		d.byUser[userID] = &userState{username: username, entry: entry, chatID: chatID, backend: backend}
		resolved++
		fmt.Printf("  • %s -> user %d (role=%s, agent=%s)\n", username, userID, entry.Role, backend)
		d.send(chatID, "🟢 Agent bridge online. Send 'help' to begin.")
	}

	if resolved == 0 && !settings.AutoReplyEnabled && !settings.Triage.Enabled {
		return fmt.Errorf("no allow-listed users could be resolved, and auto-reply/triage are off; nothing to do")
	}

	fmt.Printf("Daemon running. %d user(s) allow-listed. Listening...\n", resolved)
	fmt.Println("⚠️  SECURITY: allow-listed users can drive an AI agent on this machine.")
	fmt.Println("    Roles limit WRITES, not reads — 'read' can still exfiltrate any file this user can")
	fmt.Println("    open, and locations don't sandbox the agent. Keep the allowlist tiny and enable")
	fmt.Println("    two-factor auth on this Telegram account.")

	if DefaultAgent() == "" {
		fmt.Println("⚠️  No agent CLI (claude/codex) found — bridge sessions and triage are unavailable (auto-reply still works).")
	} else {
		fmt.Printf("agents: available=%v, bridge-default=%s, triage=%s\n", AvailableAgents(), agents.For("", ""), d.triageBackend)
	}

	if settings.AutoReplyEnabled {
		fmt.Printf("auto-reply: ON (to non-allow-listed senders)\n")
	}
	if settings.Triage.Enabled {
		if d.mainChatID == 0 {
			fmt.Println("  ! triage enabled but main_user not resolved — digests have nowhere to go")
		}
		go d.runTriageLoop()
	}

	for {
		updateJSON, err := tdlib.ReceiveUpdates(tdjson)
		if err != nil || updateJSON == "" {
			continue
		}
		u, ok := tdlib.ParseUpdateNewMessage(updateJSON)
		if !ok || u.Message.IsOutgoing {
			continue
		}
		if u.Message.SenderID.Type != "messageSenderUser" {
			continue
		}

		// A reply from anyone with an outstanding `tg ask` resolves it first,
		// taking precedence over bridge handling.
		if d.resolvePendingAsk(u.Message.SenderID.UserID, replyText(u.Message.Content)) {
			continue
		}

		d.mu.Lock()
		st, allowed := d.byUser[u.Message.SenderID.UserID]
		if allowed {
			st.chatID = u.Message.ChatID
		}
		d.mu.Unlock()
		if !allowed {
			// Not on the allow-list: auto-reply (if enabled) and buffer for triage.
			d.handleNonAllowlisted(u.Message)
			continue
		}

		if u.Message.Content.Type != "messageText" {
			d.send(st.chatID, "I can only handle text commands here.")
			continue
		}
		text := strings.TrimSpace(u.Message.Content.Text.Text)
		fmt.Printf("[bridge] %s: %s\n", st.username, snippet(text, 100))
		d.handle(st, text)
	}
}

func (d *daemon) handle(st *userState, text string) {
	if text == "" {
		return
	}
	lower := strings.ToLower(text)

	d.mu.Lock()
	busy := st.busy
	d.mu.Unlock()
	if busy {
		// Allow only status while a run is in flight.
		if lower == "status" {
			d.send(st.chatID, d.statusLine(st))
			return
		}
		d.send(st.chatID, "⏳ Still working on your previous message — one moment.")
		return
	}

	switch lower {
	case "help", "?", "/help", "start", "/start":
		d.send(st.chatID, d.helpText(st))
		return
	case "locations", "loc", "/locations":
		d.listLocations(st)
		return
	case "resume", "sessions", "/resume":
		d.listSessions(st)
		return
	case "new", "/new":
		d.startNew(st)
		return
	case "end", "stop", "exit", "/end":
		d.mu.Lock()
		st.sessionID, st.pendingKind, st.awaitingConfirm = "", "", false
		d.mu.Unlock()
		d.send(st.chatID, "Detached. Send 'locations' to pick where to work.")
		return
	case "status", "/status":
		d.send(st.chatID, d.statusLine(st))
		return
	}

	// Numeric selection from a pending menu.
	if st.pendingKind != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(text)); err == nil {
			d.selectNumber(st, n)
			return
		}
	}

	// Otherwise it's a message for the agent.
	d.mu.Lock()
	loc := st.location
	role := st.effectiveRole
	awaiting := st.awaitingConfirm
	d.mu.Unlock()
	if loc == "" {
		d.send(st.chatID, "Pick a location first — send 'locations'.")
		return
	}

	// Confirm role: a plan is awaiting your yes/no.
	if awaiting {
		switch lower {
		case "yes", "y", "go", "ok", "okay", "proceed", "do it":
			d.mu.Lock()
			st.awaitingConfirm = false
			// Execute with the user's real power so the run doesn't stall on
			// actions acceptEdits won't auto-approve (at least edit-level).
			execRole := st.entry.Role
			if execRole.rank() < RoleEdit.rank() {
				execRole = RoleEdit
			}
			d.mu.Unlock()
			d.runAgent(st, "Go ahead and carry out that plan now.", execRole, false)
		case "no", "n", "cancel", "nope", "abort":
			d.mu.Lock()
			st.awaitingConfirm = false
			d.mu.Unlock()
			d.send(st.chatID, "Cancelled. Send a new message when ready.")
		default:
			d.runAgent(st, text, RoleRead, true) // refine -> re-plan
		}
		return
	}

	if role == RoleConfirm {
		d.runAgent(st, text, RoleRead, true) // plan first, then ask
		return
	}
	d.runAgent(st, text, role, false)
}

func (d *daemon) listLocations(st *userState) {
	names := d.locations.SortedNames()
	var allowed []string
	for _, name := range names {
		if st.entry.AllowsLocation(name) {
			allowed = append(allowed, name)
		}
	}
	if len(allowed) == 0 {
		d.send(st.chatID, "No locations are configured for you. (Edit agent-locations.json / your allowlist entry.)")
		return
	}

	var b strings.Builder
	b.WriteString("📂 Locations — reply with a number:\n")
	for i, name := range allowed {
		cfg := d.locations[name]
		cap := ""
		if cfg.MaxRole.valid() {
			cap = "  [max: " + string(cfg.MaxRole) + "]"
		}
		fmt.Fprintf(&b, "  %d. %s  (%s)%s\n", i+1, name, cfg.Path, cap)
	}
	d.mu.Lock()
	st.pendingKind, st.pendingLoc, st.pendingSess = "location", allowed, nil
	d.mu.Unlock()
	d.send(st.chatID, b.String())
}

func (d *daemon) listSessions(st *userState) {
	d.mu.Lock()
	loc, path := st.location, st.locationPath
	d.mu.Unlock()
	if loc == "" {
		d.send(st.chatID, "Pick a location first — send 'locations'.")
		return
	}

	sessions, err := ListSessionsFor(st.entry.Agent, path, 12)
	if err != nil {
		d.send(st.chatID, "⚠️ Couldn't list sessions: "+err.Error())
		return
	}
	if len(sessions) == 0 {
		d.send(st.chatID, "No past sessions in "+loc+". Just send a message to start a new one.")
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "🗂 Sessions in %s — reply with a number:\n", loc)
	for i, s := range sessions {
		fmt.Fprintf(&b, "  %d. %s  (%s)\n", i+1, s.Title, humanAgo(s.Modified))
	}
	d.mu.Lock()
	st.pendingKind, st.pendingSess, st.pendingLoc = "session", sessions, nil
	d.mu.Unlock()
	d.send(st.chatID, b.String())
}

func (d *daemon) selectNumber(st *userState, n int) {
	d.mu.Lock()
	kind := st.pendingKind
	d.mu.Unlock()

	switch kind {
	case "location":
		if n < 1 || n > len(st.pendingLoc) {
			d.send(st.chatID, "Out of range. Send 'locations' again.")
			return
		}
		name := st.pendingLoc[n-1]
		cfg := d.locations[name]
		role := st.entry.Role
		if cfg.MaxRole.valid() {
			role = MinRole(role, cfg.MaxRole)
		}
		d.mu.Lock()
		st.location, st.locationPath, st.effectiveRole = name, cfg.Path, role
		st.sessionID, st.pendingKind, st.awaitingConfirm = "", "", false
		d.mu.Unlock()
		d.send(st.chatID, fmt.Sprintf("📍 %s (%s)\nRole here: %s\nSend 'resume' to continue a past session, or just send a message to start a new one.", name, cfg.Path, role))

	case "session":
		if n < 1 || n > len(st.pendingSess) {
			d.send(st.chatID, "Out of range. Send 'resume' again.")
			return
		}
		sess := st.pendingSess[n-1]
		d.mu.Lock()
		st.sessionID, st.pendingKind = sess.ID, ""
		d.mu.Unlock()
		d.send(st.chatID, fmt.Sprintf("↩️ Resuming: %s\nFetching a summary…", sess.Title))
		d.runAgent(st, "Briefly summarize in 2-4 sentences what we were last working on in this conversation and what the current state / next step is. Don't take any actions.", RoleRead, false)

	default:
		d.send(st.chatID, "Nothing to select. Send 'help'.")
	}
}

func (d *daemon) startNew(st *userState) {
	d.mu.Lock()
	loc := st.location
	st.sessionID, st.pendingKind, st.awaitingConfirm = "", "", false
	d.mu.Unlock()
	if loc == "" {
		d.send(st.chatID, "Pick a location first — send 'locations'.")
		return
	}
	d.send(st.chatID, "🆕 New session in "+loc+". Send your first message.")
}

// runAgent runs one agent turn in the background and posts the reply. When
// awaitConfirmAfter is set the run is a plan (role read) and, on success, the
// user is asked to approve before anything executes.
func (d *daemon) runAgent(st *userState, prompt string, role Role, awaitConfirmAfter bool) {
	d.mu.Lock()
	backend := st.backend
	chatID0 := st.chatID
	d.mu.Unlock()
	if backend == "" {
		d.send(chatID0, "⚠️ Agent mode is unavailable — no `claude` or `codex` CLI is installed.")
		return
	}

	d.mu.Lock()
	st.busy = true
	dir, resume, chatID := st.locationPath, st.sessionID, st.chatID
	d.mu.Unlock()

	if awaitConfirmAfter {
		d.send(chatID, "🧭 planning…")
	} else {
		d.send(chatID, "🤔 working…")
	}

	go func() {
		res, err := RunAgent(backend, dir, prompt, resume, role)

		d.mu.Lock()
		st.busy = false
		if res.SessionID != "" {
			st.sessionID = res.SessionID
		}
		if err == nil && awaitConfirmAfter {
			st.awaitingConfirm = true
		}
		d.mu.Unlock()

		if err != nil {
			if res.Text != "" {
				d.send(chatID, res.Text)
			}
			d.send(chatID, "⚠️ "+err.Error())
			return
		}
		if strings.TrimSpace(res.Text) == "" {
			d.send(chatID, "(no output)")
			return
		}
		d.send(chatID, res.Text)
		if awaitConfirmAfter {
			d.send(chatID, "✅ Reply 'yes' to carry this out, 'no' to cancel, or send changes to refine the plan.")
		}
	}()
}

func (d *daemon) statusLine(st *userState) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	loc := st.location
	if loc == "" {
		loc = "(none)"
	}
	sess := st.sessionID
	if sess == "" {
		sess = "(new on next message)"
	}
	role := st.entry.Role
	if st.location != "" {
		role = st.effectiveRole
	}
	return fmt.Sprintf("Role: %s\nLocation: %s\nSession: %s", role, loc, sess)
}

func (d *daemon) helpText(st *userState) string {
	return strings.Join([]string{
		"🤖 Agent bridge — commands:",
		"  locations — pick a project to work in",
		"  resume — continue a past session (with a summary)",
		"  new — start a fresh session here",
		"  status — show current location/session",
		"  end — detach from the current session",
		"Anything else you type is sent to the agent.",
		"",
		"Your role: " + string(st.entry.Role),
	}, "\n")
}

// send posts text to a chat, splitting anything over Telegram's length limit.
func (d *daemon) send(chatID int64, text string) {
	for _, chunk := range chunk(text, telegramMaxLen) {
		if _, err := tdlib.SendTextMessage(d.tdjson, d.clientID, chatID, chunk); err != nil {
			fmt.Printf("  ! send failed: %v\n", err)
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

func humanAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
