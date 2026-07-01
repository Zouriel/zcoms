package instagram

import (
	"database/sql"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"

	_ "modernc.org/sqlite" // pure-Go sqlite driver (registered as "sqlite")
)

// Instagram, like WhatsApp, has no queryable backlog we can lean on — the sidecar
// only surfaces recent threads. We persist every 1:1 message we see (both
// directions) into our own SQLite DB so the daemon can serve read/unread/
// mark_read, and so the poll loop can de-dupe: the UNIQUE(chat, msg_id) index
// means a message seen on an earlier poll is ignored on re-insert and never
// re-surfaces as unread after it was read.

func (t *Transport) openStore() error {
	dsn := "file:" + t.dbPath + "?_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS ig_messages (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  thread_id TEXT NOT NULL,
  msg_id    TEXT NOT NULL,
  sender    TEXT,
  from_me   INTEGER NOT NULL DEFAULT 0,
  text      TEXT,
  kind      TEXT,
  file      TEXT,
  ts        INTEGER NOT NULL,
  unread    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_ig_messages_thread ON ig_messages(thread_id, ts);
CREATE INDEX IF NOT EXISTS idx_ig_messages_unread ON ig_messages(unread);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ig_messages_unique ON ig_messages(thread_id, msg_id);`); err != nil {
		_ = db.Close()
		return err
	}
	t.mu.Lock()
	t.db = db
	t.mu.Unlock()
	return nil
}

// storeMessage records one message. Returns true when the row was newly inserted
// (not a duplicate), so the poll loop only emits an Inbound for messages it has
// not seen before. Best-effort: storage failures never break receive.
func (t *Transport) storeMessage(threadID, msgID, sender string, fromMe bool, text, kind, file string, ts time.Time, unread bool) bool {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return false
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
	res, err := db.Exec(`INSERT OR IGNORE INTO ig_messages(thread_id, msg_id, sender, from_me, text, kind, file, ts, unread)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		threadID, msgID, sender, fm, text, kind, nullStr(file), ts.Unix(), ur)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// History returns the last `count` messages of a thread, oldest-first.
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
	rows, err := db.Query(`SELECT msg_id, COALESCE(sender,''), from_me, COALESCE(text,''), COALESCE(kind,''), COALESCE(file,''), ts
		FROM ig_messages WHERE thread_id=? ORDER BY ts DESC, id DESC LIMIT ?`, chatID, count)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// Unread returns messages others sent that haven't been triaged yet, as Inbounds.
func (t *Transport) Unread() ([]transport.Inbound, error) {
	t.mu.Lock()
	db := t.db
	t.mu.Unlock()
	if db == nil {
		return nil, nil
	}
	rows, err := db.Query(`SELECT thread_id, msg_id, COALESCE(sender,''), COALESCE(text,''), COALESCE(kind,''), COALESCE(file,''), ts
		FROM ig_messages WHERE unread=1 AND from_me=0 ORDER BY ts ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []transport.Inbound
	for rows.Next() {
		var thread, msgID, sender, text, kind, file string
		var ts int64
		if err := rows.Scan(&thread, &msgID, &sender, &text, &kind, &file, &ts); err != nil {
			return nil, err
		}
		in := transport.Inbound{
			From:      transport.Address{Transport: "instagram", ID: thread},
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

// MarkRead clears the unread flag for the given messages in a thread.
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
	_, err := db.Exec(`UPDATE ig_messages SET unread=0 WHERE thread_id=? AND msg_id IN (`+ph+`)`, args...)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
