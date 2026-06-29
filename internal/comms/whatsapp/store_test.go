package whatsapp

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func storeForTest(t *testing.T) *Transport {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "wa.db") + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	tr := New("unused")
	tr.db = db
	if err := tr.initMessageStore(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return tr
}

func TestMessageStoreHistoryUnreadMarkRead(t *testing.T) {
	tr := storeForTest(t)
	base := time.Unix(1_700_000_000, 0)
	A := "111@s.whatsapp.net"
	B := "222@s.whatsapp.net"

	// Conversation with A: two from Alice (unread), one of our own replies.
	tr.storeMessage(A, "a1", "Alice", false, "hey", "messageText", "", base, true)
	tr.storeMessage(A, "me1", "you", true, "hi back", "messageText", "", base.Add(time.Minute), false)
	tr.storeMessage(A, "a2", "Alice", false, "you there?", "messageText", "", base.Add(2*time.Minute), true)
	// One from B (unread).
	tr.storeMessage(B, "b1", "Bob", false, "yo", "messageText", "", base.Add(3*time.Minute), true)

	// History(A) is oldest-first and includes our own reply.
	hist, err := tr.History(A, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 3 {
		t.Fatalf("history len = %d, want 3", len(hist))
	}
	if hist[0].Text != "hey" || hist[1].Text != "hi back" || hist[2].Text != "you there?" {
		t.Fatalf("history order wrong: %+v", hist)
	}
	if !hist[1].FromMe {
		t.Fatal("own reply should be FromMe")
	}

	// Unread = only others' unread messages (not our own), across chats.
	un, err := tr.Unread()
	if err != nil {
		t.Fatal(err)
	}
	if len(un) != 3 {
		t.Fatalf("unread len = %d, want 3 (a1,a2,b1)", len(un))
	}
	for _, u := range un {
		if u.From.Transport != "whatsapp" || u.From.ID == "" {
			t.Fatalf("unread item missing whatsapp address: %+v", u)
		}
	}

	// Mark A's messages read → only B's remains unread.
	if err := tr.MarkRead(A, []string{"a1", "a2"}); err != nil {
		t.Fatal(err)
	}
	un, _ = tr.Unread()
	if len(un) != 1 || un[0].From.ID != B {
		t.Fatalf("after mark-read want only B unread, got %+v", un)
	}
}
