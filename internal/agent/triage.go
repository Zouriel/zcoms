package agent

import (
	"fmt"
	"strings"
	"time"

	"tg/internal/tdlib"
	"tg/internal/whatsapp"
)

// triageMessage is one unread message from a non-allow-listed sender, on either
// platform. The Source-specific fields identify where to mark-read and reply.
type triageMessage struct {
	Sender string
	Text   string
	When   time.Time
	Source string // "tg" | "wa"

	TGChat int64 // set when Source=="tg"
	TGMsg  int64

	WAChat string // set when Source=="wa"
	WAMsg  string

	File string // local path to a downloaded attachment (WhatsApp), "" if none
}

// readPlan lists the messages to mark read after a triage pass, per platform.
type readPlan struct {
	TG map[int64][]int64   // tg chatID -> message IDs
	WA map[string][]string // wa chatID -> message IDs
}

// triageTimeLabel renders when a message arrived, e.g. "2h ago (Sat 14:32)".
func triageTimeLabel(t time.Time) string {
	if t.IsZero() {
		return "unknown time"
	}
	return fmt.Sprintf("%s (%s)", humanAgo(t), t.Format("Mon 15:04"))
}

// handleNonAllowlisted auto-replies (at most hourly per sender) to a stranger.
// Triage itself reads Telegram's unread state directly, so nothing is buffered.
func (d *daemon) handleNonAllowlisted(msg tdlib.Message) {
	if msg.ChatID != msg.SenderID.UserID {
		return // private chats only — never auto-reply into a group/channel
	}
	if msg.SenderID.UserID == d.mainUserID {
		return // never auto-reply to the owner
	}

	d.mu.Lock()
	last := d.lastAutoReply[msg.SenderID.UserID]
	shouldReply := d.settings.AutoReplyEnabled && d.settings.AutoReply != "" && time.Since(last) > time.Hour
	if shouldReply {
		d.lastAutoReply[msg.SenderID.UserID] = time.Now()
	}
	d.mu.Unlock()

	if shouldReply {
		d.send(msg.ChatID, d.settings.AutoReply)
	}
}

func (d *daemon) senderName(userID int64) string {
	d.mu.Lock()
	if cached, ok := d.nameCache[userID]; ok {
		d.mu.Unlock()
		return cached
	}
	d.mu.Unlock()

	name, err := tdlib.FetchUserDisplayName(d.tdjson, d.clientID, userID)
	if err != nil || name == "" {
		name = fmt.Sprintf("user:%d", userID)
	}
	d.mu.Lock()
	d.nameCache[userID] = name
	d.mu.Unlock()
	return name
}

// collectUnread scans recent chats for unread 1:1 messages from people who are
// neither the owner nor allow-listed, returning the messages plus the message
// ids to mark read (per chat) once they've been triaged.
func (d *daemon) collectUnread() ([]triageMessage, readPlan) {
	plan := readPlan{TG: map[int64][]int64{}, WA: map[string][]string{}}

	chatIDs, err := tdlib.FetchChatIdentifiers(d.tdjson, d.clientID, 80)
	if err != nil {
		fmt.Printf("[triage] couldn't list chats: %v\n", err)
		return nil, plan
	}

	d.mu.Lock()
	mainID := d.mainUserID
	d.mu.Unlock()

	var msgs []triageMessage
	seen := map[int64]bool{}

	for _, cid := range chatIDs {
		if seen[cid] {
			continue // getChats can return duplicates across pages
		}
		seen[cid] = true

		info, err := tdlib.FetchChatInfo(d.tdjson, d.clientID, cid)
		if err != nil || info.UnreadCount == 0 || info.TypeName != "private" {
			continue
		}
		if info.UserID == mainID {
			continue
		}
		d.mu.Lock()
		_, allowed := d.byUser[info.UserID]
		d.mu.Unlock()
		if allowed {
			continue // allow-listed users drive the bridge, not triage
		}
		if d.activeErrandForTG(cid) != nil {
			continue // an errand owns this conversation; don't also triage it
		}

		unread, err := tdlib.FetchUnreadIncoming(d.tdjson, d.clientID, cid, info.LastReadInboxMessageID)
		if err != nil || len(unread) == 0 {
			continue
		}

		name := d.senderName(info.UserID)
		for _, m := range unread {
			msgs = append(msgs, triageMessage{
				Sender: name,
				Text:   replyText(m.Content),
				When:   time.Unix(m.Date, 0),
				Source: "tg",
				TGChat: cid,
				TGMsg:  m.ID,
			})
			plan.TG[cid] = append(plan.TG[cid], m.ID)
		}
	}

	// Only reach for WhatsApp when explicitly enabled; on any error log and
	// continue with Telegram only so triage never breaks because of the sidecar.
	if d.settings.WhatsApp.Enabled {
		wa, err := whatsapp.FetchUnread(d.settings.WhatsApp.Socket)
		if err != nil {
			fmt.Printf("[triage] whatsapp unavailable, skipping (tg only): %v\n", err)
		} else {
			for _, u := range wa {
				if d.activeErrandForWA(u.ChatID) != nil {
					continue // an errand owns this WhatsApp chat; don't also triage it
				}
				msgs = append(msgs, triageMessage{
					Sender: u.Sender,
					Text:   u.Text,
					When:   u.When,
					Source: "wa",
					WAChat: u.ChatID,
					WAMsg:  u.MsgID,
					File:   u.File,
				})
				plan.WA[u.ChatID] = append(plan.WA[u.ChatID], u.MsgID)
			}
		}
	}

	return msgs, plan
}

// runTriageLoop runs an initial pass shortly after start, then follows the
// configured schedule. The schedule is re-read from disk each cycle, so
// `tg triage <schedule>` (and on/off) take effect without a restart.
func (d *daemon) runTriageLoop() {
	fmt.Printf("[triage] %s, agent=%s, dir=%s\n", d.settings.Triage.Describe(), d.triageBackend, d.settings.Triage.Dir)

	// Initial pass shortly after start (if enabled).
	time.Sleep(3 * time.Minute)
	if tri := d.currentTriage(); tri.Enabled {
		d.runTriageOnce(tri.Dir)
	}

	// Poll so `tg triage <schedule>` (and on/off) take effect within ~30s.
	lastKey := ""
	nextRun := time.Now()
	for {
		time.Sleep(30 * time.Second)
		tri := d.currentTriage()
		if !tri.Enabled {
			lastKey = ""
			continue
		}
		key := strings.ToLower(strings.TrimSpace(tri.Schedule))
		if key != lastKey {
			lastKey = key
			nextRun = tri.NextRun(time.Now()) // schedule changed -> reschedule
		}
		if !time.Now().Before(nextRun) {
			d.runTriageOnce(tri.Dir)
			nextRun = tri.NextRun(time.Now())
		}
	}
}

// currentTriage reloads the triage settings from disk, falling back to the
// startup values if the file can't be read.
func (d *daemon) currentTriage() TriageSettings {
	if s, _, err := LoadOrSeedSettings(); err == nil {
		return s.Triage
	}
	return d.settings.Triage
}

func (d *daemon) runTriageOnce(dir string) {
	if d.mainChatID == 0 {
		return // no main_user resolved — nowhere to send; don't process/mark-read
	}

	msgs, plan := d.collectUnread()
	if len(msgs) == 0 {
		return
	}
	if d.triageBackend == "" {
		fmt.Printf("[triage] %d unread message(s) but no agent installed; leaving unread\n", len(msgs))
		return
	}

	var b strings.Builder
	b.WriteString("You are triaging unread messages received for the owner while they were away.\n")
	b.WriteString("Decide which are IMPORTANT enough to notify them now (urgent, personal, time-sensitive, ")
	b.WriteString("or someone clearly needing a reply). Ignore spam, promotions, automated/bot noise, and trivial chatter.\n")
	b.WriteString("If NONE are important, reply with exactly: NONE\n")
	b.WriteString("Otherwise reply with a short bullet list, one per important message, and START each bullet with when it arrived: '• <when> — <sender>: <one-line why it matters>'.\n")
	b.WriteString("Do not take any actions or run any commands.\n\nMessages:\n")
	for i, m := range msgs {
		fmt.Fprintf(&b, "%d. [%s] [received %s] From %s: %s", i+1, platformLabel(m.Source), triageTimeLabel(m.When), m.Sender, snippet(m.Text, 300))
		if m.File != "" {
			fmt.Fprintf(&b, " (attachment saved: %s)", m.File)
		}
		b.WriteString("\n")
	}

	// Resume the persistent triage brain so it accumulates memory across passes
	// (until explicitly reset). Serialize with any `interact triage` turn.
	prevID, _ := LoadTriageSessionID()
	// Yield to an active `interact triage`/`chat` turn rather than queueing behind
	// it (an interactive turn can hold the brain lock across several agent rounds).
	// The messages stay unread, so the next cycle re-triages them.
	if !d.triageMu.TryLock() {
		fmt.Println("[triage] skipped this pass — an interactive triage/chat turn is active; will retry next cycle")
		return
	}
	res, err := RunAgent(d.triageBackend, dir, b.String(), prevID, RoleRead, false)
	if res.SessionID != "" {
		_ = SaveTriageSessionID(res.SessionID)
	}
	d.triageMu.Unlock()
	if err != nil {
		fmt.Printf("[triage] agent error (leaving unread to retry): %v\n", err)
		return
	}

	var bullets []string
	for _, line := range strings.Split(res.Text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "•") || strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			bullets = append(bullets, t)
		}
	}

	// Notify the owner first; if the digest fails to send, leave everything
	// unread so the next pass retries (don't lose important messages).
	if len(bullets) > 0 {
		digest := "\U0001F4E8 Messages worth your attention:\n" + strings.Join(bullets, "\n") +
			"\n\nReply `interact triage` to act on these."
		if err := d.sendErr(d.mainChatID, digest); err != nil {
			fmt.Printf("[triage] digest send failed (leaving unread to retry): %v\n", err)
			return
		}
	}

	// Delivered (or nothing important) -> mark read so they aren't re-triaged.
	read := 0
	for chatID, ids := range plan.TG {
		if err := tdlib.MarkMessagesRead(d.tdjson, d.clientID, chatID, ids); err != nil {
			fmt.Printf("[triage] couldn't mark read in chat %d: %v\n", chatID, err)
		} else {
			read += len(ids)
		}
	}
	if d.settings.WhatsApp.Enabled {
		for chatID, ids := range plan.WA {
			// Read silently by default (no blue ticks); only send WhatsApp read
			// receipts if the owner explicitly opted in. Either way the messages
			// leave the sidecar's unread mirror so they aren't re-triaged.
			var err error
			if d.settings.WhatsApp.ReadReceipts {
				err = whatsapp.MarkRead(d.settings.WhatsApp.Socket, chatID, ids)
			} else {
				err = whatsapp.Dismiss(d.settings.WhatsApp.Socket, chatID, ids)
			}
			if err != nil {
				fmt.Printf("[triage] couldn't clear whatsapp unread in %s: %v\n", chatID, err)
			} else {
				read += len(ids)
			}
		}
	}

	// Persist the batch regardless of how many bullets there were — `interact
	// triage` should be able to reply to anyone who wrote in, even if the AI
	// didn't flag them. A persist failure is non-fatal (just log it).
	if err := SaveTriageBatch(buildTriageBatch(msgs, time.Now())); err != nil {
		fmt.Printf("[triage] couldn't persist batch: %v\n", err)
	}

	if len(bullets) == 0 {
		fmt.Printf("[triage] %d unread message(s) read, none important\n", read)
	} else {
		fmt.Printf("[triage] %d unread message(s) read, digest of %d sent\n", read, len(bullets))
	}
}

// platformLabel renders a triage source for the digest prompt.
func platformLabel(source string) string {
	if source == "wa" {
		return "WhatsApp"
	}
	return "Telegram"
}
