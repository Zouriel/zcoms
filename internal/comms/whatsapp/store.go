package whatsapp

import (
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"
)

// The WhatsApp transport keeps its own message history because whatsmeow exposes
// messages only as live events — there is no queryable backlog. We persist every
// 1:1 message (both directions) into zc_messages in the same SQLite DB as the
// device session, so the daemon can serve read/unread/mark_read for WhatsApp the
// way it does for Telegram.

func (t *Transport) initMessageStore() error {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return nil
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS zc_messages (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  chat_jid  TEXT NOT NULL,
  msg_id    TEXT,
  sender    TEXT,
  from_me   INTEGER NOT NULL DEFAULT 0,
  text      TEXT,
  kind      TEXT,
  file      TEXT,
  ts        INTEGER NOT NULL,
  unread    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_zc_messages_chat ON zc_messages(chat_jid, ts);
CREATE INDEX IF NOT EXISTS idx_zc_messages_unread ON zc_messages(unread);
-- One row per (chat, message id): a redelivered message (on reconnect / history
-- sync) is ignored on insert, so it never re-surfaces as unread after being read.
CREATE UNIQUE INDEX IF NOT EXISTS idx_zc_messages_unique ON zc_messages(chat_jid, msg_id);`)
	return err
}

// storeMessage records one 1:1 message. Best-effort: a storage failure must
// never break send/receive, so errors are swallowed (the transport keeps going).
func (t *Transport) storeMessage(chatJID, msgID, sender string, fromMe bool, text, kind, file string, ts time.Time, unread bool) {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return
	}
	fm, ur := 0, 0
	if fromMe {
		fm = 1
	}
	if unread {
		ur = 1
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	_, _ = db.Exec(`INSERT OR IGNORE INTO zc_messages(chat_jid, msg_id, sender, from_me, text, kind, file, ts, unread)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		chatJID, msgID, sender, fm, text, kind, nullStr(file), ts.Unix(), ur)
}

// markReadByIDs clears the unread flag for the given message ids across all
// chats (WhatsApp message ids are globally unique, so no chat is needed). Used
// when the owner reads a chat on another device (a read receipt), so triage
// never digests a message the owner already saw.
func (t *Transport) markReadByIDs(ids []string) {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil || len(ids) == 0 {
		return
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	_, _ = db.Exec(`UPDATE zc_messages SET unread=0 WHERE msg_id IN (`+ph+`)`, args...)
}

// History returns the last `count` messages of a chat, oldest-first.
func (t *Transport) History(chatID string, count int) ([]transport.HistMessage, error) {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return nil, nil
	}
	if count <= 0 {
		count = 20
	}
	rows, err := db.Query(`SELECT COALESCE(msg_id,''), COALESCE(sender,''), from_me, COALESCE(text,''), COALESCE(kind,''), COALESCE(file,''), ts
		FROM zc_messages WHERE chat_jid=? ORDER BY ts DESC, id DESC LIMIT ?`, chatID, count)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []transport.HistMessage
	for rows.Next() {
		var m transport.HistMessage
		var fromMe int
		var ts int64
		if err := rows.Scan(&m.MessageID, &m.Sender, &fromMe, &m.Text, &m.Kind, &m.File, &ts); err != nil {
			return nil, err
		}
		m.FromMe = fromMe != 0
		m.At = time.Unix(ts, 0)
		out = append(out, m)
	}
	// Reverse to oldest-first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// Unread returns messages others sent that haven't been triaged yet, as Inbounds
// (so the hub maps them like any inbound).
func (t *Transport) Unread() ([]transport.Inbound, error) {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT chat_jid, COALESCE(msg_id,''), COALESCE(sender,''), COALESCE(text,''), COALESCE(kind,''), COALESCE(file,''), ts
		FROM zc_messages WHERE unread=1 AND from_me=0 ORDER BY ts ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []transport.Inbound
	for rows.Next() {
		var chat, msgID, sender, text, kind, file string
		var ts int64
		if err := rows.Scan(&chat, &msgID, &sender, &text, &kind, &file, &ts); err != nil {
			return nil, err
		}
		in := transport.Inbound{
			From:      transport.Address{Transport: "whatsapp", ID: chat},
			Sender:    sender,
			Text:      text,
			Kind:      kind,
			MessageID: msgID,
			At:        time.Unix(ts, 0),
		}
		if file != "" {
			in.Files = []string{file}
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// MarkRead clears the unread flag for the given messages in a chat.
func (t *Transport) MarkRead(chatID string, msgIDs []string) error {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil || len(msgIDs) == 0 {
		return nil
	}
	ph := strings.TrimRight(strings.Repeat("?,", len(msgIDs)), ",")
	args := make([]any, 0, len(msgIDs)+1)
	args = append(args, chatID)
	for _, id := range msgIDs {
		args = append(args, id)
	}
	_, err := db.Exec(`UPDATE zc_messages SET unread=0 WHERE chat_jid=? AND msg_id IN (`+ph+`)`, args...)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
