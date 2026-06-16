package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// last-triage.json persists the most recent triage batch so `interact triage`
// can reply to whoever wrote in — immediately after a digest or much later, and
// across daemon restarts. It is overwritten each triage run.
const lastTriageFile = "last-triage.json"

// Recipient is one person from the last triage batch, addressable only by Index
// so the interactive-reply agent can never message an arbitrary contact.
type Recipient struct {
	Index    int      `json:"index"`  // 1-based, stable within a batch
	Source   string   `json:"source"` // "tg" | "wa"
	Name     string   `json:"name"`
	TGChat   int64    `json:"tg_chat,omitempty"`
	WAChat   string   `json:"wa_chat,omitempty"`
	Messages []string `json:"messages"`        // their unread text(s), for context
	Files    []string `json:"files,omitempty"` // local paths to attachments they sent
}

// TriageBatch is the deduped recipient table from one triage run.
type TriageBatch struct {
	At         time.Time   `json:"at"`
	Recipients []Recipient `json:"recipients"`
}

// buildTriageBatch groups the run's messages into a deduped recipient table,
// one entry per sender, with stable 1-based indices in first-seen order.
func buildTriageBatch(msgs []triageMessage, at time.Time) TriageBatch {
	var recipients []Recipient
	// key -> position in recipients (so repeated messages from one person merge).
	pos := map[string]int{}
	for _, m := range msgs {
		key := m.Source + "\x00"
		if m.Source == "wa" {
			key += m.WAChat
		} else {
			key += strconv.FormatInt(m.TGChat, 10)
		}
		if i, ok := pos[key]; ok {
			recipients[i].Messages = append(recipients[i].Messages, m.Text)
			if m.File != "" {
				recipients[i].Files = append(recipients[i].Files, m.File)
			}
			continue
		}
		pos[key] = len(recipients)
		rec := Recipient{
			Index:    len(recipients) + 1,
			Source:   m.Source,
			Name:     m.Sender,
			TGChat:   m.TGChat,
			WAChat:   m.WAChat,
			Messages: []string{m.Text},
		}
		if m.File != "" {
			rec.Files = []string{m.File}
		}
		recipients = append(recipients, rec)
	}
	return TriageBatch{At: at, Recipients: recipients}
}

// SaveTriageBatch writes the batch to last-triage.json (0600, overwritten).
func SaveTriageBatch(b TriageBatch) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, lastTriageFile), b)
}

// LoadTriageBatch reads the last persisted batch. A missing file is not an
// error — it returns an empty batch so callers can say "nothing to act on yet".
func LoadTriageBatch() (TriageBatch, error) {
	dir, err := configDir()
	if err != nil {
		return TriageBatch{}, err
	}
	data, err := os.ReadFile(filepath.Join(dir, lastTriageFile))
	if errors.Is(err, os.ErrNotExist) {
		return TriageBatch{}, nil
	}
	if err != nil {
		return TriageBatch{}, err
	}
	var b TriageBatch
	if err := json.Unmarshal(data, &b); err != nil {
		return TriageBatch{}, err
	}
	return b, nil
}
