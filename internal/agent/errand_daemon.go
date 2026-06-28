package agent

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/tdlib"
	"github.com/Zouriel/zcoms/internal/whatsapp"
)

// errandPollInterval is how often the daemon checks WhatsApp for replies to
// active errands. WhatsApp has no push (the sidecar is poll-only), so this is
// the only thing that advances a WhatsApp errand. Telegram errands advance in
// real time from dispatchUpdate; this poll is just a backstop for them.
const errandPollInterval = 25 * time.Second

// activeErrandForTG returns the running errand targeting a Telegram chat, if any.
func (d *daemon) activeErrandForTG(chatID int64) *Errand {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range d.errands {
		if e.Source != "wa" && e.TGChat == chatID && e.active() {
			return e
		}
	}
	return nil
}

// activeErrandForWA returns the running errand targeting a WhatsApp chat, if any.
func (d *daemon) activeErrandForWA(jid string) *Errand {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range d.errands {
		if e.Source == "wa" && e.WAChat == jid && e.active() {
			return e
		}
	}
	return nil
}

// routeTGErrandReply feeds a Telegram message into its errand: dedupe, mark
// read, download any attachment, and advance the errand.
func (d *daemon) routeTGErrandReply(e *Errand, msg tdlib.Message) {
	id := strconv.FormatInt(msg.ID, 10)
	d.mu.Lock()
	fresh := e.markSeen(id)
	d.mu.Unlock()
	if !fresh {
		return
	}
	_ = SaveErrand(e)

	if err := tdlib.MarkMessagesRead(d.tdjson, d.clientID, msg.ChatID, []int64{msg.ID}); err != nil {
		fmt.Printf("[errand %s] couldn't mark read: %v\n", e.ID, err)
	}

	// Only the interviewer consumes replies. Once we're producing (or done), the
	// contact's messages are marked read above but otherwise ignored.
	if !e.interviewing() {
		return
	}

	media := ""
	if msg.Content.Type != "messageText" {
		media = d.downloadMessageMedia(msg)
	}
	fmt.Printf("[errand %s] %s replied\n", e.ID, e.TargetName)
	d.feedErrand(e, replyText(msg.Content), media)
}

// runErrandLoop polls WhatsApp for replies to active WhatsApp errands and feeds
// them in, marking them handled so triage never re-processes them.
func (d *daemon) runErrandLoop() {
	for {
		time.Sleep(errandPollInterval)
		if !d.settings.WhatsApp.Enabled {
			continue
		}
		if !d.hasActiveWAErrand() {
			continue
		}
		unread, err := whatsapp.FetchUnread(d.settings.WhatsApp.Socket)
		if err != nil {
			continue // sidecar down; try again next tick
		}
		// Group this chat's unread per errand, in arrival order.
		handled := map[string][]string{} // jid -> msg ids to clear
		for _, u := range unread {
			e := d.activeErrandForWA(u.ChatID)
			if e == nil {
				continue
			}
			d.mu.Lock()
			fresh := e.markSeen(u.MsgID)
			d.mu.Unlock()
			handled[u.ChatID] = append(handled[u.ChatID], u.MsgID)
			// Mark handled regardless so triage won't see it; only feed the
			// interviewer (producing/done phases ignore contact replies).
			if !fresh || !e.interviewing() {
				continue
			}
			_ = SaveErrand(e)
			fmt.Printf("[errand %s] %s replied (WhatsApp)\n", e.ID, e.TargetName)
			d.feedErrand(e, u.Text, u.File)
		}
		// Clear the errand's messages from the sidecar's unread mirror so triage
		// won't see them. Respect the read-receipt setting like triage does.
		for jid, ids := range handled {
			if d.settings.WhatsApp.ReadReceipts {
				_ = whatsapp.MarkRead(d.settings.WhatsApp.Socket, jid, ids)
			} else {
				_ = whatsapp.Dismiss(d.settings.WhatsApp.Socket, jid, ids)
			}
		}
	}
}

func (d *daemon) hasActiveWAErrand() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range d.errands {
		if e.Source == "wa" && e.active() {
			return true
		}
	}
	return false
}

// ErrandSpec is a request to start an errand, before target resolution.
type ErrandSpec struct {
	Target          string // "@user" | chat id | "wa:<number|jid>" | "#<batch index>"
	Brief           string
	DeliverToTarget bool
	AutoStart       bool
}

// startErrand resolves the target, creates and persists the errand, and kicks
// it off (approval draft, or straight into the conversation when AutoStart).
// Returns a short confirmation line for the dispatcher.
func (d *daemon) startErrand(spec ErrandSpec) (string, error) {
	if d.mainChatID == 0 {
		return "", fmt.Errorf("no main user resolved — set main_user in agent-settings.json so I know where to report back")
	}
	if strings.TrimSpace(spec.Brief) == "" {
		return "", fmt.Errorf("an errand needs a brief (what should I ask / produce?)")
	}

	e := &Errand{
		ID:              newErrandID(),
		Status:          ErrandPendingApproval,
		Brief:           strings.TrimSpace(spec.Brief),
		OwnerChat:       d.mainChatID,
		DeliverToTarget: spec.DeliverToTarget,
		AutoStart:       spec.AutoStart,
		CreatedAt:       time.Now(),
	}
	if spec.AutoStart {
		e.Status = ErrandActive
	}

	if err := d.resolveErrandTarget(e, spec.Target); err != nil {
		return "", err
	}

	d.mu.Lock()
	d.errands[e.ID] = e
	d.mu.Unlock()
	if err := SaveErrand(e); err != nil {
		return "", fmt.Errorf("couldn't save errand: %w", err)
	}

	d.kickErrand(e)

	verb := "Drafting a plan for your approval"
	if e.AutoStart {
		verb = "Starting now"
	}
	return fmt.Sprintf("🗂 Errand %s → %s (%s). %s.", e.ID, e.TargetName, e.platform(), verb), nil
}

// resolveErrandTarget fills in the errand's Source + chat from a raw target
// token: "#N" picks recipient N from the last triage batch (carries the right
// platform); "wa:..." or a WhatsApp jid is WhatsApp; "@user" or a numeric id is
// Telegram.
func (d *daemon) resolveErrandTarget(e *Errand, target string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("an errand needs a target contact")
	}

	// Batch index: "#2" -> recipient 2 from the last triage batch.
	if strings.HasPrefix(target, "#") {
		idx, err := strconv.Atoi(strings.TrimPrefix(target, "#"))
		if err != nil {
			return fmt.Errorf("bad batch index %q", target)
		}
		batch, err := LoadTriageBatch()
		if err != nil {
			return fmt.Errorf("couldn't load the last triage batch: %w", err)
		}
		for _, r := range batch.Recipients {
			if r.Index == idx {
				e.Source, e.TargetName, e.TGChat, e.WAChat = r.Source, r.Name, r.TGChat, r.WAChat
				return nil
			}
		}
		return fmt.Errorf("no recipient #%d in the last triage batch", idx)
	}

	// WhatsApp: explicit "wa:" prefix or a jid shape.
	if strings.HasPrefix(target, "wa:") || strings.Contains(target, "@s.whatsapp.net") || strings.Contains(target, "@lid") {
		jid := normalizeWAJID(strings.TrimPrefix(target, "wa:"))
		e.Source = "wa"
		e.WAChat = jid
		e.TargetName = jid
		// Try to give it a friendlier name from the last batch.
		if batch, err := LoadTriageBatch(); err == nil {
			for _, r := range batch.Recipients {
				if r.Source == "wa" && r.WAChat == jid {
					e.TargetName = r.Name
					break
				}
			}
		}
		return nil
	}

	// Telegram: @username or numeric chat id.
	chatID, _, err := d.resolveChat(target)
	if err != nil {
		return fmt.Errorf("couldn't resolve %q: %w", target, err)
	}
	e.Source = "tg"
	e.TGChat = chatID
	e.TargetName = target
	if name := d.senderName(chatID); name != "" && !strings.HasPrefix(name, "user:") {
		e.TargetName = name
	}
	return nil
}

func normalizeWAJID(target string) string {
	target = strings.TrimSpace(target)
	if target == "" || strings.Contains(target, "@") {
		return target
	}
	return target + "@s.whatsapp.net"
}

// pendingErrand returns the single errand awaiting the owner's approval, or the
// one matching id when given. Used by the `errand yes/no/edit` bridge commands.
func (d *daemon) pendingErrand(id string) *Errand {
	d.mu.Lock()
	defer d.mu.Unlock()
	var match *Errand
	count := 0
	for _, e := range d.errands {
		if e.Status != ErrandPendingApproval {
			continue
		}
		if id != "" && e.ID == id {
			return e
		}
		match = e
		count++
	}
	if id == "" && count == 1 {
		return match
	}
	return nil
}

// cancelErrand stops an errand (by id) so the contact stops being messaged and
// triage/auto-reply resume for them.
func (d *daemon) cancelErrand(id string) (*Errand, bool) {
	d.mu.Lock()
	e, ok := d.errands[id]
	if ok {
		e.Status = ErrandCancelled
	}
	d.mu.Unlock()
	if ok {
		_ = SaveErrand(e)
	}
	return e, ok
}

// errandExists reports whether an errand id is known.
func (d *daemon) errandExists(id string) bool {
	d.mu.Lock()
	_, ok := d.errands[id]
	d.mu.Unlock()
	return ok
}

// reviseErrandPlan feeds the owner's requested changes back to a pending errand
// and re-drafts the plan for approval (it stays pending).
func (d *daemon) reviseErrandPlan(e *Errand, changes string) {
	prompt := fmt.Sprintf("The owner wants these changes before you start: %s\n\n%s",
		strings.TrimSpace(changes), errandApprovalPrompt(e))
	d.driveErrandAsync(e, prompt)
}

// handleErrandCommand forwards the owner's `errand …` bridge command to the
// external errands component and relays its reply.
func (d *daemon) handleErrandCommand(st *userState, text string) {
	handled, reply, err := ErrandsCommand(text)
	if !handled {
		d.send(st.chatID, "The errands component isn't running — install it with `zc install errands`.")
		return
	}
	if err != nil {
		d.send(st.chatID, "⚠️ "+err.Error())
		return
	}
	d.send(st.chatID, reply)
}

// parseErrandStart parses the part after "start": "[deliver] [go] <target> | <brief>".
func parseErrandStart(s string) (ErrandSpec, error) {
	i := strings.Index(s, "|")
	if i < 0 {
		return ErrandSpec{}, fmt.Errorf("usage: errand start [deliver] [go] <@user|wa:JID|#index> | <brief>")
	}
	left := strings.Fields(strings.TrimSpace(s[:i]))
	spec := ErrandSpec{Brief: strings.TrimSpace(s[i+1:])}
	var target string
	for _, tok := range left {
		switch strings.ToLower(tok) {
		case "deliver":
			spec.DeliverToTarget = true
		case "go", "now", "auto":
			spec.AutoStart = true
		default:
			if target == "" {
				target = tok
			} else {
				target += " " + tok
			}
		}
	}
	spec.Target = target
	if target == "" {
		return spec, fmt.Errorf("no target contact given")
	}
	return spec, nil
}

// errandListText renders the active errands for the owner.
func (d *daemon) errandListText() string {
	d.mu.Lock()
	var active []*Errand
	for _, e := range d.errands {
		if e.active() {
			active = append(active, e)
		}
	}
	d.mu.Unlock()
	if len(active) == 0 {
		return "No active errands."
	}
	var b strings.Builder
	b.WriteString("🗂 Active errands:\n")
	for _, e := range active {
		fmt.Fprintf(&b, "  %s → %s (%s) [%s] — %s\n", e.ID, e.TargetName, e.platform(), e.Status, snippet(e.Brief, 80))
	}
	return b.String()
}
