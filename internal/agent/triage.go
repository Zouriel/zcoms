package agent

import (
	"fmt"
	"strings"
	"time"

	"tg/internal/tdlib"
)

// triageMessage is one unread message from a non-allow-listed sender.
type triageMessage struct {
	Sender string
	Text   string
	When   time.Time
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
func (d *daemon) collectUnread() ([]triageMessage, map[int64][]int64) {
	chatIDs, err := tdlib.FetchChatIdentifiers(d.tdjson, d.clientID, 80)
	if err != nil {
		fmt.Printf("[triage] couldn't list chats: %v\n", err)
		return nil, nil
	}

	d.mu.Lock()
	mainID := d.mainUserID
	d.mu.Unlock()

	var msgs []triageMessage
	toRead := map[int64][]int64{}
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

		unread, err := tdlib.FetchUnreadIncoming(d.tdjson, d.clientID, cid, info.LastReadInboxMessageID)
		if err != nil || len(unread) == 0 {
			continue
		}

		name := d.senderName(info.UserID)
		for _, m := range unread {
			msgs = append(msgs, triageMessage{Sender: name, Text: replyText(m.Content), When: time.Unix(m.Date, 0)})
			toRead[cid] = append(toRead[cid], m.ID)
		}
	}
	return msgs, toRead
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

	msgs, toRead := d.collectUnread()
	if len(msgs) == 0 {
		return
	}
	if d.triageBackend == "" {
		fmt.Printf("[triage] %d unread message(s) but no agent installed; leaving unread\n", len(msgs))
		return
	}

	var b strings.Builder
	b.WriteString("You are triaging unread Telegram messages received for the owner while they were away.\n")
	b.WriteString("Decide which are IMPORTANT enough to notify them now (urgent, personal, time-sensitive, ")
	b.WriteString("or someone clearly needing a reply). Ignore spam, promotions, automated/bot noise, and trivial chatter.\n")
	b.WriteString("If NONE are important, reply with exactly: NONE\n")
	b.WriteString("Otherwise reply with a short bullet list, one per important message, and START each bullet with when it arrived: '• <when> — <sender>: <one-line why it matters>'.\n")
	b.WriteString("Do not take any actions or run any commands.\n\nMessages:\n")
	for i, m := range msgs {
		fmt.Fprintf(&b, "%d. [received %s] From %s: %s\n", i+1, triageTimeLabel(m.When), m.Sender, snippet(m.Text, 300))
	}

	res, err := RunAgent(d.triageBackend, dir, b.String(), "", RoleRead)
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
		if err := d.sendErr(d.mainChatID, "\U0001F4E8 Messages worth your attention:\n"+strings.Join(bullets, "\n")); err != nil {
			fmt.Printf("[triage] digest send failed (leaving unread to retry): %v\n", err)
			return
		}
	}

	// Delivered (or nothing important) -> mark read so they aren't re-triaged.
	read := 0
	for chatID, ids := range toRead {
		if err := tdlib.MarkMessagesRead(d.tdjson, d.clientID, chatID, ids); err != nil {
			fmt.Printf("[triage] couldn't mark read in chat %d: %v\n", chatID, err)
		} else {
			read += len(ids)
		}
	}

	if len(bullets) == 0 {
		fmt.Printf("[triage] %d unread message(s) read, none important\n", read)
	} else {
		fmt.Printf("[triage] %d unread message(s) read, digest of %d sent\n", read, len(bullets))
	}
}
