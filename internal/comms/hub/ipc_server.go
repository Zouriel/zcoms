package hub

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
)

// serveIPC opens the Unix socket upper tiers connect to in order to reuse the
// daemon's single Telegram session and the contacts directory.
func (d *daemon) serveIPC() error {
	path, err := client.DefaultSocketPath()
	if err != nil {
		return err
	}
	_ = os.Remove(path) // clear a stale socket from a previous run

	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	// Restrict to the owner: 0600 means only our user can route through the daemon.
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
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[ipc] recovered from panic: %v\n", r)
			writeIPC(conn, client.Response{Error: fmt.Sprintf("internal error: %v", r)})
		}
	}()

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return
	}

	var req client.Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeIPC(conn, client.Response{Error: "bad request"})
		return
	}

	// Protocol handshake: reject a caller that speaks a different wire version,
	// loudly, before doing anything. Absent (0) = a legacy caller making no claim.
	if req.Version != 0 && req.Version != client.ProtocolVersion {
		writeIPC(conn, client.Response{
			Version: client.ProtocolVersion,
			Error:   fmt.Sprintf("protocol mismatch: daemon speaks v%d, caller speaks v%d", client.ProtocolVersion, req.Version),
		})
		return
	}

	switch req.Op {
	case "hello":
		writeIPC(conn, client.Response{OK: true, Version: client.ProtocolVersion})

	case "send":
		chatID, _, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		msgID, err := telegram.SendTextMessage(d.tdjson, d.clientID, chatID, req.Text)
		if err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		writeIPC(conn, client.Response{OK: true, MessageID: msgID, ChatID: chatID})

	case "sendfile":
		chatID, _, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		// Fire-and-forget: the completion arrives as an unsolicited update the
		// receive loop consumes, so waiting here would race. The daemon stays
		// alive, so the upload finishes in the background regardless.
		_, label, err := telegram.SendLocalFileMessage(d.tdjson, d.clientID, chatID, req.Path, req.Text)
		if err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		writeIPC(conn, client.Response{OK: true, ChatID: chatID, Label: label})

	case "read":
		chatID, _, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		count := req.Count
		if count <= 0 {
			count = 10
		}
		if count > maxIPCReadCount {
			count = maxIPCReadCount
		}
		history, err := telegram.FetchChatHistorySnapshot(d.tdjson, d.clientID, chatID, count)
		if err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		// History comes newest-first; emit oldest-first so it reads naturally.
		msgs := make([]client.Message, 0, len(history))
		titleCache := map[int64]string{}
		downloads := 0
		for i := len(history) - 1; i >= 0; i-- {
			dl := req.Download && downloads < maxReadDownloads
			m := d.buildMessage(history[i], titleCache, dl)
			if m.File != "" {
				downloads++
			}
			msgs = append(msgs, m)
		}
		writeIPC(conn, client.Response{OK: true, ChatID: chatID, Messages: msgs})

	case "ask":
		chatID, userID, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		// Empty text = `zc tg chat` "just listen" mode: don't send, only wait.
		if req.Text != "" {
			if _, err := telegram.SendTextMessage(d.tdjson, d.clientID, chatID, req.Text); err != nil {
				writeIPC(conn, client.Response{Error: err.Error()})
				return
			}
		}

		replyCh := make(chan string, 1)
		d.mu.Lock()
		d.pendingAsk[userID] = append(d.pendingAsk[userID], replyCh)
		d.mu.Unlock()

		clientGone := make(chan struct{})
		go func() {
			buf := make([]byte, 1)
			_, _ = conn.Read(buf)
			close(clientGone)
		}()

		select {
		case reply := <-replyCh:
			writeIPC(conn, client.Response{OK: true, Reply: reply})
		case <-clientGone:
			d.removePending(userID, replyCh)
		}

	case "resolve":
		chatID, _, err := d.resolveChat(req.To)
		if err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		writeIPC(conn, client.Response{OK: true, ChatID: chatID})

	case "unread":
		writeIPC(conn, client.Response{OK: true, Unread: d.collectUnreadTG()})

	case "mark_read":
		if req.ChatID == 0 || len(req.MessageIDs) == 0 {
			writeIPC(conn, client.Response{Error: "mark_read needs chat_id and message_ids"})
			return
		}
		if err := telegram.MarkMessagesRead(d.tdjson, d.clientID, req.ChatID, req.MessageIDs); err != nil {
			writeIPC(conn, client.Response{Error: err.Error()})
			return
		}
		writeIPC(conn, client.Response{OK: true, ChatID: req.ChatID})

	case "subscribe":
		d.serveSubscription(conn, req.Role)
		return

	// --- contacts directory (comms.db) ---------------------------------------
	case "contact_resolve", "contact_list", "contact_create", "contact_update",
		"contact_delete", "contact_upsert", "contact_handle_add", "contact_handle_remove":
		d.handleContactOp(conn, req)

	default:
		writeIPC(conn, client.Response{Error: "unknown op: " + req.Op})
	}
}

func writeIPC(conn net.Conn, resp client.Response) {
	b, _ := json.Marshal(resp)
	_, _ = conn.Write(append(b, '\n'))
}
