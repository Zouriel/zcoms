package instagram

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"
)

// fakeSidecar is an in-memory stand-in for the Python REST sidecar so we can
// exercise the whole login/poll/send state machine without Instagram.
type fakeSidecar struct {
	lastSend  map[string]string
	pending   string // "needs_2fa" once a login parks on 2FA
	threads   []thread
	dumpCalls int
	loggedIn  bool
}

func (f *fakeSidecar) server() *httptest.Server {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) { _ = json.NewEncoder(w).Encode(v) }

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		f.pending = "needs_2fa"
		write(w, loginResult{Status: "needs_2fa"})
	})
	mux.HandleFunc("/login/verify", func(w http.ResponseWriter, r *http.Request) {
		f.pending = ""
		f.loggedIn = true
		write(w, loginResult{Status: "ok"})
	})
	mux.HandleFunc("/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			f.dumpCalls++
			write(w, map[string]any{"settings": json.RawMessage(`{"uuid":"x"}`)})
			return
		}
		// POST restore: pretend the blob is invalid so tests drive a fresh login.
		write(w, loginResult{Status: "error", Message: "expired"})
	})
	mux.HandleFunc("/threads", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"threads": f.threads})
	})
	mux.HandleFunc("/user_id", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]string{"user_id": "999"})
	})
	mux.HandleFunc("/send", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.lastSend = body
		write(w, sendResult{MessageID: "m-sent", ThreadID: "t-1"})
	})
	mux.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		f.loggedIn = false
		write(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { write(w, healthResult{OK: true, LoggedIn: f.loggedIn}) })
	return httptest.NewServer(mux)
}

func newTestTransport(t *testing.T, base string) *Transport {
	t.Helper()
	dir := t.TempDir()
	tr := New(dir)
	tr.sc = newSidecarClient(base)
	tr.dbPath = filepath.Join(dir, "instagram", "messages.db")
	if err := os.MkdirAll(filepath.Dir(tr.dbPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := tr.openStore(); err != nil {
		t.Fatalf("openStore: %v", err)
	}
	return tr
}

// A login that parks on 2FA, then a submitted code, drives connected + a session
// dump/save.
func TestLoginTwoFactorThenCode(t *testing.T) {
	f := &fakeSidecar{}
	srv := f.server()
	defer srv.Close()
	tr := newTestTransport(t, srv.URL)
	tr.cfg = Config{Username: "me", Password: "pw"}

	tr.startLogin(t.Context())
	if st := tr.Status(); st.State != transport.StateActionRequired || st.Detail != transport.Needs2FA {
		t.Fatalf("after login want action_required/needs_2fa, got %+v", st)
	}

	if err := tr.Action("code_123456"); err != nil {
		t.Fatalf("submit code: %v", err)
	}
	if st := tr.Status(); st.State != transport.StateConnected {
		t.Fatalf("after code want connected, got %+v", st)
	}
	// The session was persisted (dumped + encrypted to disk).
	if f.dumpCalls == 0 {
		t.Fatal("expected a session dump after login")
	}
	if blob, err := tr.sess.Load(); err != nil || len(blob) == 0 {
		t.Fatalf("session not saved: %v (%d bytes)", err, len(blob))
	}
}

// Polling stores + emits each inbound once; a second poll of the same thread
// emits nothing (deduped), and own messages are never emitted.
func TestPollDedupAndSelfExclusion(t *testing.T) {
	f := &fakeSidecar{
		threads: []thread{{
			ThreadID: "t-1", Username: "rani",
			Messages: []message{
				{ID: "m-1", Text: "hey", Timestamp: float64(time.Now().Unix())},
				{ID: "m-2", Text: "me talking", IsFromMe: true, Timestamp: float64(time.Now().Unix())},
			},
		}},
	}
	srv := f.server()
	defer srv.Close()
	tr := newTestTransport(t, srv.URL)
	tr.setStatus(transport.StateConnected, "")
	ch := make(chan transport.Inbound, 8)
	tr.inbound = ch

	tr.pollOnce(t.Context())
	tr.pollOnce(t.Context()) // second pass must add nothing new

	if len(ch) != 1 {
		t.Fatalf("want exactly 1 emitted inbound, got %d", len(ch))
	}
	in := <-ch
	if in.MessageID != "m-1" || in.From.Transport != "instagram" || in.From.ID != "t-1" || in.Sender != "rani" {
		t.Fatalf("bad inbound: %+v", in)
	}

	// Unread reflects the one other-party message; MarkRead clears it.
	un, err := tr.Unread()
	if err != nil || len(un) != 1 {
		t.Fatalf("unread = %v (%d)", err, len(un))
	}
	if err := tr.MarkRead("t-1", []string{"m-1"}); err != nil {
		t.Fatalf("markread: %v", err)
	}
	if un, _ := tr.Unread(); len(un) != 0 {
		t.Fatalf("still unread after markread: %d", len(un))
	}
}

// Sending to a @handle resolves the user id; replying to a thread id sends by
// thread.
func TestSendResolvesHandleAndThread(t *testing.T) {
	f := &fakeSidecar{}
	srv := f.server()
	defer srv.Close()
	tr := newTestTransport(t, srv.URL)
	tr.setStatus(transport.StateConnected, "")

	if _, err := tr.Send(transport.Address{Transport: "instagram", ID: "@rani"}, "hi"); err != nil {
		t.Fatalf("send handle: %v", err)
	}
	if f.lastSend["user_id"] != "999" || f.lastSend["thread_id"] != "" {
		t.Fatalf("handle send should use resolved user_id, got %+v", f.lastSend)
	}

	if _, err := tr.Send(transport.Address{Transport: "instagram", ID: "t-1"}, "reply"); err != nil {
		t.Fatalf("send thread: %v", err)
	}
	if f.lastSend["thread_id"] != "t-1" || f.lastSend["user_id"] != "" {
		t.Fatalf("thread send should use thread_id, got %+v", f.lastSend)
	}

	// The own outbound is recorded (from_me), so it shows in history but not unread.
	if un, _ := tr.Unread(); len(un) != 0 {
		t.Fatalf("own sends must not be unread: %d", len(un))
	}
}

func TestSendBeforeConnectedFails(t *testing.T) {
	tr := newTestTransport(t, "http://127.0.0.1:0")
	if _, err := tr.Send(transport.Address{ID: "t-1"}, "x"); err == nil {
		t.Fatal("send before connected should fail")
	}
}
