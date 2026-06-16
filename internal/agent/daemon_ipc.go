package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"zcoms/internal/tdlib"
)

// serveIPC opens the Unix socket that `zc tg send`/`zc tg ask` connect to so they can
// reuse the daemon's single Telegram session instead of opening their own.
func (d *daemon) serveIPC() error {
	path, err := SocketPath()
	if err != nil {
		return err
	}
	_ = os.Remove(path) // clear a stale socket from a previous run

	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	// Restrict to the owner: a Unix socket needs write permission to connect, so
	// 0600 means only our user can route send/ask through the daemon.
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		return err
	}
	fmt.Println("ipc socket:", path)

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go d.handleIPC(conn)
		}
	}()
	return nil
}

func (d *daemon) handleIPC(conn net.Conn) {
	defer conn.Close()
	// A panic in any op (e.g. parsing odd TDLib content during a read) must not
	// crash the whole daemon — surface it as an error response instead.
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[ipc] recovered from panic: %v\n", r)
			writeIPC(conn, ipcResponse{Error: fmt.Sprintf("internal error: %v", r)})
		}
	}()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}

	var req ipcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeIPC(conn, ipcResponse{Error: "bad request"})
		return
	}
	fmt.Printf("[ipc] %s -> %s\n", req.Op, req.To)

	switch req.Op {
	case "send":
		chatID, _, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		msgID, err := tdlib.SendTextMessage(d.tdjson, d.clientID, chatID, req.Text)
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		writeIPC(conn, ipcResponse{OK: true, MessageID: msgID, ChatID: chatID})

	case "sendfile":
		chatID, _, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		// Fire-and-forget: don't WaitForSendCompletion here. The completion
		// arrives as an unsolicited update that the daemon's main receive loop
		// would consume, so waiting on it would race/hang. The daemon stays
		// alive, so the upload finishes in the background regardless.
		_, label, err := tdlib.SendLocalFileMessage(d.tdjson, d.clientID, chatID, req.Path, req.Text)
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		writeIPC(conn, ipcResponse{OK: true, ChatID: chatID, Label: label})

	case "read":
		chatID, _, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		count := req.Count
		if count <= 0 {
			count = 10
		}
		if count > maxIPCReadCount {
			count = maxIPCReadCount // never let one read pull an unbounded history
		}
		history, err := tdlib.FetchChatHistorySnapshot(d.tdjson, d.clientID, chatID, count)
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		// History comes newest-first; emit oldest-first so it reads naturally.
		msgs := make([]IPCMessage, 0, len(history))
		titleCache := map[int64]string{}
		downloads := 0
		for i := len(history) - 1; i >= 0; i-- {
			dl := req.Download && downloads < maxReadDownloads
			m := d.buildIPCMessage(history[i], titleCache, dl)
			if m.File != "" {
				downloads++
			}
			msgs = append(msgs, m)
		}
		writeIPC(conn, ipcResponse{OK: true, ChatID: chatID, Messages: msgs})

	case "ask":
		chatID, userID, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		// Empty text = `zc tg chat` "just listen" mode: don't send, only wait.
		if req.Text != "" {
			if _, err := tdlib.SendTextMessage(d.tdjson, d.clientID, chatID, req.Text); err != nil {
				writeIPC(conn, ipcResponse{Error: err.Error()})
				return
			}
		}

		replyCh := make(chan string, 1)
		d.mu.Lock()
		d.pendingAsk[userID] = append(d.pendingAsk[userID], replyCh)
		d.mu.Unlock()

		// Detect the client disconnecting so a dead `ask` doesn't later swallow
		// one of the user's bridge messages.
		clientGone := make(chan struct{})
		go func() {
			buf := make([]byte, 1)
			_, _ = conn.Read(buf)
			close(clientGone)
		}()

		select {
		case reply := <-replyCh:
			writeIPC(conn, ipcResponse{OK: true, Reply: reply})
		case <-clientGone:
			d.removePending(userID, replyCh)
		}

	case "resolve":
		chatID, _, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		writeIPC(conn, ipcResponse{OK: true, ChatID: chatID})

	case "unread":
		// Expose the same Telegram inbox the daemon would triage, so the
		// external triage component can read it over IPC.
		msgs, _ := d.collectUnreadTG()
		items := make([]UnreadItem, 0, len(msgs))
		for _, m := range msgs {
			items = append(items, UnreadItem{
				Sender: m.Sender, Text: m.Text, When: m.When.Unix(),
				ChatID: m.TGChat, MsgID: m.TGMsg,
			})
		}
		writeIPC(conn, ipcResponse{OK: true, Unread: items})

	case "mark_read":
		if req.ChatID == 0 || len(req.MessageIDs) == 0 {
			writeIPC(conn, ipcResponse{Error: "mark_read needs chat_id and message_ids"})
			return
		}
		if err := tdlib.MarkMessagesRead(d.tdjson, d.clientID, req.ChatID, req.MessageIDs); err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		writeIPC(conn, ipcResponse{OK: true, ChatID: req.ChatID})

	case "subscribe":
		d.serveSubscription(conn, req.Role)
		return

	case "errand_start":
		msg, err := d.startErrand(ErrandSpec{
			Target: req.To, Brief: req.Brief, DeliverToTarget: req.Deliver, AutoStart: req.AutoStart,
		})
		if err != nil {
			writeIPC(conn, ipcResponse{Error: err.Error()})
			return
		}
		writeIPC(conn, ipcResponse{OK: true, Reply: msg})

	case "errand_list":
		writeIPC(conn, ipcResponse{OK: true, Reply: d.errandListText()})

	case "errand_cancel":
		e, ok := d.cancelErrand(req.ID)
		if !ok {
			writeIPC(conn, ipcResponse{Error: "no errand with id " + req.ID})
			return
		}
		writeIPC(conn, ipcResponse{OK: true, Reply: "Cancelled errand " + e.ID})

	default:
		writeIPC(conn, ipcResponse{Error: "unknown op: " + req.Op})
	}
}

func writeIPC(conn net.Conn, resp ipcResponse) {
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
}

// maxReadDownloads caps how many media files one read fetches, so a snapshot of
// a media-heavy chat can't trigger an unbounded run of blocking downloads.
const maxReadDownloads = 8

// maxIPCReadCount caps how many messages one read op pulls, so an over-eager
// `zc tg chat --read N` (e.g. an agent reading a huge unread thread) can't blow up
// the daemon's memory/time paging an entire conversation.
const maxIPCReadCount = 200

// downloadMessageMedia downloads a message's attachment (if any) and returns its
// local path within TDLib's cache, or "" when there's nothing to fetch / it failed.
func (d *daemon) downloadMessageMedia(m tdlib.Message) string {
	f, _, _, ok := m.Content.MediaFile()
	if !ok || f.ID == 0 {
		return ""
	}
	if f.Local.IsDownloadingCompleted && f.Local.Path != "" {
		return f.Local.Path
	}
	path, err := tdlib.DownloadFile(d.tdjson, d.clientID, f.ID, 90*time.Second)
	if err != nil {
		return ""
	}
	return path
}

// buildIPCMessage renders a history message into the wire shape the CLI prints,
// resolving the sender's display name (users via the daemon's shared name cache,
// chats via the per-request titleCache) and the media kind/label. When download
// is set, an attachment is fetched and its local path included.
func (d *daemon) buildIPCMessage(m tdlib.Message, titleCache map[int64]string, download bool) IPCMessage {
	sender := "unknown"
	switch m.SenderID.Type {
	case "messageSenderUser":
		sender = d.senderName(m.SenderID.UserID)
	case "messageSenderChat":
		cid := m.SenderID.ChatID
		if cached, ok := titleCache[cid]; ok && cached != "" {
			sender = cached
		} else if title, err := tdlib.FetchChatTitle(d.tdjson, d.clientID, cid); err == nil && title != "" {
			titleCache[cid] = title
			sender = title
		} else {
			sender = fmt.Sprintf("chat:%d", cid)
		}
	}

	kind := "text"
	if m.Content.Type != "messageText" {
		if _, _, label, isMedia := m.Content.MediaFile(); isMedia {
			kind = label
		} else {
			kind = strings.TrimPrefix(m.Content.Type, "message")
		}
	}

	file := ""
	if download && kind != "text" {
		file = d.downloadMessageMedia(m)
	}

	return IPCMessage{
		MessageID: m.ID,
		ChatID:    m.ChatID,
		Date:      m.Date,
		Outgoing:  m.IsOutgoing,
		Sender:    sender,
		Kind:      kind,
		Text:      m.Content.CaptionOrText(),
		File:      file,
	}
}

// resolveChat turns "@username" or a numeric id into a chat id (and user id).
func (d *daemon) resolveChat(to string) (chatID, userID int64, err error) {
	to = strings.TrimSpace(to)
	if id, e := strconv.ParseInt(to, 10, 64); e == nil {
		return id, id, nil // private chat id == user id in TDLib
	}
	uid, e := tdlib.ResolveUserIdentifierByUsername(d.tdjson, d.clientID, to)
	if e != nil {
		return 0, 0, e
	}
	cid, e := tdlib.CreatePrivateChat(d.tdjson, d.clientID, uid)
	if e != nil {
		cid = uid
	}
	return cid, uid, nil
}

// resolvePendingAsk delivers text to the oldest outstanding `zc tg ask` for userID,
// returning true if one was waiting.
func (d *daemon) resolvePendingAsk(userID int64, text string) bool {
	d.mu.Lock()
	queue := d.pendingAsk[userID]
	if len(queue) == 0 {
		d.mu.Unlock()
		return false
	}
	ch := queue[0]
	d.pendingAsk[userID] = queue[1:]
	d.mu.Unlock()

	ch <- text
	return true
}

func (d *daemon) removePending(userID int64, ch chan string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	queue := d.pendingAsk[userID]
	for i, c := range queue {
		if c == ch {
			d.pendingAsk[userID] = append(queue[:i], queue[i+1:]...)
			return
		}
	}
}

// replyText extracts a user's reply for `zc tg ask`, matching standalone behavior
// (text, else caption, else a [type] tag for media).
func replyText(c tdlib.Content) string {
	if c.Type == "messageText" {
		return c.Text.Text
	}
	if t := c.CaptionOrText(); t != "" {
		return t
	}
	return "[" + c.Type + "]"
}
