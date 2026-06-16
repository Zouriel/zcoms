package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"time"
)

// The daemon owns the single Telegram session. When it's running, `tg send`/
// `tg ask` route their request through this Unix socket instead of opening their
// own client (which would deadlock on the session lock). When it's absent, the
// commands fall back to talking to Telegram directly.

const socketName = "daemon.sock"

func SocketPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, socketName), nil
}

type ipcRequest struct {
	Op       string `json:"op"`                 // "send" | "ask" | "sendfile" | "read" | "errand_*"
	To       string `json:"to"`                 // @username or numeric chat id / errand target
	Text     string `json:"text"`               // message body / question / caption
	Path     string `json:"path,omitempty"`     // local file path (sendfile)
	Count    int    `json:"count,omitempty"`    // number of history messages (read)
	Download bool   `json:"download,omitempty"` // download media in a read

	// Errand ops.
	Brief     string `json:"brief,omitempty"`      // what to ask / produce (errand_start)
	Deliver   bool   `json:"deliver,omitempty"`    // also send the deliverable to the contact
	AutoStart bool   `json:"auto_start,omitempty"` // skip the approval step (errand_start)
	ID        string `json:"id,omitempty"`         // errand id (errand_cancel)
}

// IPCMessage is one history message returned by the daemon's "read" op. It
// mirrors the fields `tg chat` prints so the CLI can render it identically
// whether the daemon answered or it talked to Telegram directly.
type IPCMessage struct {
	MessageID int64  `json:"message_id"`
	ChatID    int64  `json:"chat_id"`
	Date      int64  `json:"date"`
	Outgoing  bool   `json:"outgoing"`
	Sender    string `json:"sender"`
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	File      string `json:"file,omitempty"` // local path if media was downloaded
}

type ipcResponse struct {
	OK        bool         `json:"ok"`
	MessageID int64        `json:"message_id,omitempty"`
	ChatID    int64        `json:"chat_id,omitempty"`
	Reply     string       `json:"reply,omitempty"`
	Label     string       `json:"label,omitempty"`
	Messages  []IPCMessage `json:"messages,omitempty"`
	Error     string       `json:"error,omitempty"`
}

func dialDaemon() (net.Conn, bool) {
	path, err := SocketPath()
	if err != nil {
		return nil, false
	}
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, false
	}
	return conn, true
}

func roundTrip(req ipcRequest, readDeadline time.Time) (ipcResponse, bool, error) {
	conn, ok := dialDaemon()
	if !ok {
		return ipcResponse{}, false, nil // no daemon → caller falls back
	}
	defer conn.Close()

	line, err := json.Marshal(req)
	if err != nil {
		return ipcResponse{}, true, err
	}
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return ipcResponse{}, true, err
	}

	if !readDeadline.IsZero() {
		_ = conn.SetReadDeadline(readDeadline)
	}
	respLine, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(respLine) == 0 {
		return ipcResponse{}, true, err
	}

	var resp ipcResponse
	if err := json.Unmarshal(respLine, &resp); err != nil {
		return ipcResponse{}, true, err
	}
	if !resp.OK {
		return resp, true, errors.New(resp.Error)
	}
	return resp, true, nil
}

// DaemonSend sends a one-way message through a running daemon. handled is false
// when no daemon is listening, so the caller should send directly instead.
func DaemonSend(to, text string) (handled bool, msgID, chatID int64, err error) {
	resp, handled, err := roundTrip(ipcRequest{Op: "send", To: to, Text: text}, time.Now().Add(30*time.Second))
	return handled, resp.MessageID, resp.ChatID, err
}

// DaemonAsk sends a question through a running daemon and blocks until the user
// replies (no timeout, matching standalone `tg ask`). handled is false when no
// daemon is listening.
func DaemonAsk(to, text string) (handled bool, reply string, err error) {
	resp, handled, err := roundTrip(ipcRequest{Op: "ask", To: to, Text: text}, time.Time{})
	return handled, resp.Reply, err
}

// DaemonChatWait routes a `tg chat` turn through the daemon: it optionally sends
// text (empty = just listen) and waits for the user's next reply, up to timeout
// (0 = no limit). handled is false when no daemon is listening.
func DaemonChatWait(to, text string, timeout time.Duration) (handled bool, reply string, err error) {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	resp, handled, err := roundTrip(ipcRequest{Op: "ask", To: to, Text: text}, deadline)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return handled, "", fmt.Errorf("timed out after %s waiting for a reply", timeout)
	}
	return handled, resp.Reply, err
}

// DaemonRead fetches the last `count` history messages of a chat through a
// running daemon (which owns the single Telegram session). handled is false when
// no daemon is listening, so the caller can read directly instead.
func DaemonRead(to string, count int, download bool) (handled bool, msgs []IPCMessage, err error) {
	deadline := time.Now().Add(60 * time.Second)
	if download {
		deadline = time.Now().Add(5 * time.Minute) // media downloads can take a while
	}
	resp, handled, err := roundTrip(ipcRequest{Op: "read", To: to, Count: count, Download: download}, deadline)
	return handled, resp.Messages, err
}

// DaemonSendFile sends a local file through a running daemon and waits for the
// upload to finish. handled is false when no daemon is listening.
func DaemonSendFile(to, path, caption string) (handled bool, label string, chatID int64, err error) {
	resp, handled, err := roundTrip(ipcRequest{Op: "sendfile", To: to, Path: path, Text: caption}, time.Now().Add(31*time.Minute))
	return handled, resp.Label, resp.ChatID, err
}

// DaemonErrandStart dispatches an errand through a running daemon (which owns
// the Telegram session and drives the errand). handled is false when no daemon
// is listening — errands only exist inside the daemon, so the caller should say
// so rather than fall back.
func DaemonErrandStart(target, brief string, deliver, autoStart bool) (handled bool, reply string, err error) {
	resp, handled, err := roundTrip(ipcRequest{
		Op: "errand_start", To: target, Brief: brief, Deliver: deliver, AutoStart: autoStart,
	}, time.Now().Add(30*time.Second))
	return handled, resp.Reply, err
}

// DaemonErrandList lists active errands through a running daemon.
func DaemonErrandList() (handled bool, reply string, err error) {
	resp, handled, err := roundTrip(ipcRequest{Op: "errand_list"}, time.Now().Add(10*time.Second))
	return handled, resp.Reply, err
}

// DaemonErrandCancel cancels an errand by id through a running daemon.
func DaemonErrandCancel(id string) (handled bool, reply string, err error) {
	resp, handled, err := roundTrip(ipcRequest{Op: "errand_cancel", ID: id}, time.Now().Add(10*time.Second))
	return handled, resp.Reply, err
}

// DaemonRunning reports whether a bridge daemon is listening on the socket.
func DaemonRunning() bool {
	conn, ok := dialDaemon()
	if ok {
		_ = conn.Close()
	}
	return ok
}
