package contacts

import (
	"fmt"
	"strings"
)

// ThreadTimer is one thread's disappearing timer: messages in the thread
// older than Seconds, and newer than SinceID, are deleted by the daemon.
type ThreadTimer struct {
	Transport string
	NativeID  string // the group
	ThreadID  string
	Seconds   int32
	SinceID   int64 // newest message already swept (or the newest when the timer was set)
}

// SetThreadTimer sets a thread's timer. sinceID is where sweeping starts:
// messages at or before it stay, so the timer applies to messages sent from
// now on, as Telegram's own timer does. seconds 0 removes the timer.
func (s *Store) SetThreadTimer(transportName, nativeID, threadID string, seconds int32, sinceID int64) error {
	nativeID, threadID = strings.TrimSpace(nativeID), strings.TrimSpace(threadID)
	if nativeID == "" || threadID == "" {
		return fmt.Errorf("a thread timer needs the group and the thread")
	}
	if seconds <= 0 {
		_, err := s.db.Exec(`DELETE FROM thread_timers WHERE transport=? AND native_id=? AND thread_id=?`, transportName, nativeID, threadID)
		return err
	}
	_, err := s.db.Exec(`INSERT INTO thread_timers(transport, native_id, thread_id, seconds, since_id, updated_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(transport, native_id, thread_id) DO UPDATE SET seconds=excluded.seconds, updated_at=excluded.updated_at`,
		transportName, nativeID, threadID, seconds, sinceID, now())
	return err
}

// AdvanceThreadTimer records that a thread was swept up to sinceID. It never
// moves the mark backwards.
func (s *Store) AdvanceThreadTimer(transportName, nativeID, threadID string, sinceID int64) error {
	_, err := s.db.Exec(`UPDATE thread_timers SET since_id=? WHERE transport=? AND native_id=? AND thread_id=? AND since_id<?`,
		sinceID, transportName, nativeID, threadID, sinceID)
	return err
}

// ThreadTimers lists the thread timers of one group, or of every group when
// nativeID is empty.
func (s *Store) ThreadTimers(transportName, nativeID string) ([]ThreadTimer, error) {
	q := `SELECT transport, native_id, thread_id, seconds, since_id FROM thread_timers WHERE transport=?`
	args := []any{transportName}
	if nativeID != "" {
		q += ` AND native_id=?`
		args = append(args, nativeID)
	}
	rows, err := s.db.Query(q+` ORDER BY native_id, thread_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ThreadTimer
	for rows.Next() {
		var t ThreadTimer
		if err := rows.Scan(&t.Transport, &t.NativeID, &t.ThreadID, &t.Seconds, &t.SinceID); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
