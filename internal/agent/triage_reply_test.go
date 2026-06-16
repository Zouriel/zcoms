package agent

import (
	"strings"
	"testing"
	"time"
)

// TestTriageSessionRoundTrip covers the persistent triage-brain session store:
// load (empty) -> save -> load -> reset -> load (empty).
func TestTriageSessionRoundTrip(t *testing.T) {
	// Be polite to any real state: restore whatever was there afterwards.
	orig, _ := LoadTriageSessionID()
	t.Cleanup(func() {
		if orig == "" {
			_ = ResetTriageSession()
		} else {
			_ = SaveTriageSessionID(orig)
		}
	})

	if err := SaveTriageSessionID("sess-abc-123"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got, _ := LoadTriageSessionID(); got != "sess-abc-123" {
		t.Fatalf("after save: got %q want sess-abc-123", got)
	}
	if err := ResetTriageSession(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got, _ := LoadTriageSessionID(); got != "" {
		t.Fatalf("after reset: got %q want empty", got)
	}
}

// TestBuildTriageBatchDedup verifies repeated messages from one sender collapse
// into a single indexed recipient, with platform fields preserved.
func TestBuildTriageBatchDedup(t *testing.T) {
	msgs := []triageMessage{
		{Sender: "Alice", Text: "are we still on?", Source: "wa", WAChat: "111@s.whatsapp.net"},
		{Sender: "Bob", Text: "sent the file", Source: "tg", TGChat: 222},
		{Sender: "Alice", Text: "?", Source: "wa", WAChat: "111@s.whatsapp.net"},
	}
	b := buildTriageBatch(msgs, time.Now())
	if len(b.Recipients) != 2 {
		t.Fatalf("got %d recipients want 2", len(b.Recipients))
	}
	alice := b.Recipients[0]
	if alice.Index != 1 || alice.Name != "Alice" || alice.Source != "wa" || alice.WAChat != "111@s.whatsapp.net" {
		t.Fatalf("alice recipient wrong: %+v", alice)
	}
	if len(alice.Messages) != 2 {
		t.Fatalf("alice should have 2 messages, got %v", alice.Messages)
	}
	bob := b.Recipients[1]
	if bob.Index != 2 || bob.Source != "tg" || bob.TGChat != 222 {
		t.Fatalf("bob recipient wrong: %+v", bob)
	}
}

// TestSendDirective checks the SEND-line parser. The target is now any non-space
// token — a batch index, a @username, or a numeric chat id — resolved downstream.
func TestSendDirective(t *testing.T) {
	cases := map[string]struct {
		match  bool
		target string
	}{
		"SEND 1 | hello there":    {true, "1"},
		"SEND 12 |  spaced ":      {true, "12"},
		"SEND @raani | hi there":  {true, "@raani"},
		"SEND -100200300 | group": {true, "-100200300"},
		"send 1 | lowercase":      {false, ""}, // case-sensitive directive
		"just a normal message":   {false, ""},
		"SEND | missing target":   {false, ""},
	}
	for line, want := range cases {
		m := sendDirective.FindStringSubmatch(line)
		if want.match != (m != nil) {
			t.Errorf("%q: match=%v want %v", line, m != nil, want.match)
			continue
		}
		if want.match && m[1] != want.target {
			t.Errorf("%q: target=%q want %q", line, m[1], want.target)
		}
	}
}

// TestParseReadDirectives covers the READ-line parser the daemon uses to decide
// which chats to fetch on the sandboxed agent's behalf.
func TestParseReadDirectives(t *testing.T) {
	text := strings.Join([]string{
		"Let me check those.",
		"READ @raani",         // default count
		"READ 12345 5",        // explicit count
		"READ @bob 999",       // clamped to 50
		"read @nope",          // wrong case — ignored
		"SEND 1 | not a read", // not a read
	}, "\n")

	reads := parseReadDirectives(text)
	if len(reads) != 3 {
		t.Fatalf("got %d reads want 3: %+v", len(reads), reads)
	}
	if reads[0].Target != "@raani" || reads[0].Count != 10 {
		t.Errorf("read0 = %+v want {@raani 10}", reads[0])
	}
	if reads[1].Target != "12345" || reads[1].Count != 5 {
		t.Errorf("read1 = %+v want {12345 5}", reads[1])
	}
	if reads[2].Target != "@bob" || reads[2].Count != 50 {
		t.Errorf("read2 = %+v want {@bob 50} (clamped)", reads[2])
	}
}

// TestSendFileDirective covers the SENDFILE parser + path/caption split.
func TestSendFileDirective(t *testing.T) {
	cases := map[string]struct {
		match   bool
		target  string
		path    string
		caption string
	}{
		"SENDFILE 1 | /tmp/a.png":              {true, "1", "/tmp/a.png", ""},
		"SENDFILE @raani | shot.png | here ya": {true, "@raani", "shot.png", "here ya"},
		"SENDFILE 6244 | ~/x.pdf|doc":          {true, "6244", "~/x.pdf", "doc"},
		"SEND 1 | not a file":                  {false, "", "", ""},
		"sendfile 1 | nope":                    {false, "", "", ""}, // case-sensitive
	}
	for line, want := range cases {
		m := sendFileDirective.FindStringSubmatch(line)
		if want.match != (m != nil) {
			t.Errorf("%q: match=%v want %v", line, m != nil, want.match)
			continue
		}
		if !want.match {
			continue
		}
		path, caption := splitFileArg(m[2])
		if m[1] != want.target || path != want.path || caption != want.caption {
			t.Errorf("%q -> target=%q path=%q caption=%q want %q/%q/%q", line, m[1], path, caption, want.target, want.path, want.caption)
		}
	}
}
