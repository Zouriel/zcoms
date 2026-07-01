package instagram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// sidecarClient is a thin HTTP client for the aiograpi/instagrapi REST sidecar
// (see contrib/instagram-sidecar/). Instagram has no official API, so all real
// work happens in the Python sidecar behind the private-API library; the Go side
// only orchestrates login state, polls threads, and sends. Every call is short
// and JSON in/out.
type sidecarClient struct {
	http *http.Client
	base string
}

func newSidecarClient(base string) *sidecarClient {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		base = "http://127.0.0.1:8099"
	}
	return &sidecarClient{base: base, http: &http.Client{Timeout: 60 * time.Second}}
}

// loginResult mirrors the sidecar's login/verify response. Status is one of
// "ok" | "needs_2fa" | "needs_challenge" | "needs_code" | "error".
type loginResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type healthResult struct {
	OK       bool `json:"ok"`
	LoggedIn bool `json:"logged_in"`
}

// thread and message mirror the sidecar's /threads shape. IDs are strings.
type message struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	Text      string  `json:"text"`
	ItemType  string  `json:"item_type"`
	Timestamp float64 `json:"timestamp"` // unix seconds
	IsFromMe  bool    `json:"is_from_me"`
}

type thread struct {
	ThreadID string    `json:"thread_id"`
	Title    string    `json:"title"`
	Username string    `json:"username"` // the other party's handle (1:1)
	Messages []message `json:"messages"`
	IsGroup  bool      `json:"is_group"`
}

func (s *sidecarClient) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		// Surface the sidecar's error message when it sent one.
		var e struct {
			Detail  string `json:"detail"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Detail
		if msg == "" {
			msg = e.Message
		}
		if msg == "" {
			msg = strings.TrimSpace(string(data))
		}
		return fmt.Errorf("sidecar %s %s: %s (%d)", method, path, msg, resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (s *sidecarClient) Health(ctx context.Context) (healthResult, error) {
	var h healthResult
	err := s.do(ctx, http.MethodGet, "/health", nil, &h)
	return h, err
}

// Login starts a fresh username/password login. The result status tells the
// caller whether Instagram demanded a second factor or a challenge.
func (s *sidecarClient) Login(ctx context.Context, user, pass, proxy string) (loginResult, error) {
	var r loginResult
	err := s.do(ctx, http.MethodPost, "/login",
		map[string]string{"username": user, "password": pass, "proxy": proxy}, &r)
	return r, err
}

// SubmitCode resolves a pending 2FA or challenge with the code the owner entered.
func (s *sidecarClient) SubmitCode(ctx context.Context, code string) (loginResult, error) {
	var r loginResult
	err := s.do(ctx, http.MethodPost, "/login/verify", map[string]string{"code": code}, &r)
	return r, err
}

// RestoreSession loads a previously dumped settings blob into the sidecar and
// verifies it is still valid. A non-nil error (or status!=ok) means re-login.
func (s *sidecarClient) RestoreSession(ctx context.Context, settings []byte) (loginResult, error) {
	var r loginResult
	err := s.do(ctx, http.MethodPost, "/settings",
		map[string]any{"settings": json.RawMessage(settings)}, &r)
	return r, err
}

// DumpSession returns the sidecar's current settings so we can persist it.
func (s *sidecarClient) DumpSession(ctx context.Context) ([]byte, error) {
	var raw struct {
		Settings json.RawMessage `json:"settings"`
	}
	if err := s.do(ctx, http.MethodGet, "/settings", nil, &raw); err != nil {
		return nil, err
	}
	return raw.Settings, nil
}

func (s *sidecarClient) Logout(ctx context.Context) error {
	return s.do(ctx, http.MethodPost, "/logout", nil, nil)
}

// Threads returns up to amount recent direct threads, each with its last few
// messages, for the receive poll.
func (s *sidecarClient) Threads(ctx context.Context, amount int) ([]thread, error) {
	var out struct {
		Threads []thread `json:"threads"`
	}
	err := s.do(ctx, http.MethodGet, fmt.Sprintf("/threads?amount=%d", amount), nil, &out)
	return out.Threads, err
}

// ResolveUser maps a @handle to a numeric user id (for opening a new DM).
func (s *sidecarClient) ResolveUser(ctx context.Context, username string) (string, error) {
	var out struct {
		UserID string `json:"user_id"`
	}
	err := s.do(ctx, http.MethodGet, "/user_id?username="+username, nil, &out)
	return out.UserID, err
}

type sendResult struct {
	MessageID string `json:"message_id"`
	ThreadID  string `json:"thread_id"`
}

// SendText sends a direct message. Exactly one of threadID/userID is used
// (threadID wins when both are set — a reply to an existing conversation).
func (s *sidecarClient) SendText(ctx context.Context, threadID, userID, text string) (sendResult, error) {
	var r sendResult
	err := s.do(ctx, http.MethodPost, "/send",
		map[string]string{"thread_id": threadID, "user_id": userID, "text": text}, &r)
	return r, err
}

// SendFile uploads a local file (photo/video/document per the sidecar) to a DM.
func (s *sidecarClient) SendFile(ctx context.Context, threadID, userID, path, caption string) (sendResult, error) {
	var r sendResult
	err := s.do(ctx, http.MethodPost, "/send_file",
		map[string]string{"thread_id": threadID, "user_id": userID, "path": path, "caption": caption}, &r)
	return r, err
}
