package hub

import (
	"errors"
	"fmt"
	"net"
	"strconv"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/comms/transport"
)

// handleMessageOp serves the Telegram message and chat ops: edit, pin, unpin
// and unpin_all (a zero message id means the newest message this account sent
// there), disappear and disappear_get (the chat's disappearing timer), and
// send_keep (a send that never disappears).
func (d *daemon) handleMessageOp(conn net.Conn, req client.Request) {
	resp, err := d.messageOp(req)
	if err != nil {
		resp.Error = err.Error()
		writeIPC(conn, resp)
		return
	}
	resp.OK = true
	writeIPC(conn, resp)
}

func (d *daemon) messageOp(req client.Request) (client.Response, error) {
	if transportName(req) != "telegram" {
		return client.Response{}, errors.New(req.Op + " works only on Telegram")
	}
	chatID, _, err := d.resolveChat(req.To)
	if err != nil {
		return client.Response{}, err
	}
	resp := client.Response{ChatID: chatID}
	switch req.Op {
	case "unpin_all":
		return resp, telegram.UnpinAllMessages(d.tdjson, d.clientID, chatID)
	case "disappear_get":
		resp.Disappear, err = telegram.AutoDelete(d.tdjson, d.clientID, chatID)
		return resp, err
	case "disappear":
		if req.Disappear < 0 {
			return resp, errors.New("the timer cannot be negative")
		}
		resp.Disappear = req.Disappear
		return resp, telegram.SetAutoDelete(d.tdjson, d.clientID, chatID, req.Disappear)
	case "send_keep":
		return d.sendKeep(req, chatID)
	}

	msgID := req.MessageID
	if msgID == 0 {
		if msgID, err = telegram.LastOutgoingID(d.tdjson, d.clientID, chatID); err != nil {
			return resp, err
		}
	}
	resp.MessageID = msgID

	switch req.Op {
	case "edit":
		if req.Text == "" {
			return resp, errors.New("edit needs the new text")
		}
		return resp, telegram.EditMessageText(d.tdjson, d.clientID, chatID, msgID, req.Text)
	case "pin":
		return resp, telegram.PinMessage(d.tdjson, d.clientID, chatID, msgID, req.Silent, req.ForSelf)
	case "unpin":
		return resp, telegram.UnpinMessage(d.tdjson, d.clientID, chatID, msgID)
	}
	return resp, errors.New("unknown op: " + req.Op)
}

// sendKeep sends a message that does not disappear, optionally pinned. A
// message takes the chat's disappearing timer when it is sent and pinning does
// not exempt it, so in a disappearing chat the timer is turned off for this
// one send and put back afterwards, whatever happens. Messages others send in
// that moment do not disappear either.
func (d *daemon) sendKeep(req client.Request, chatID int64) (client.Response, error) {
	resp := client.Response{ChatID: chatID}
	if req.Text == "" {
		return resp, errors.New("send_keep needs the text")
	}
	rs, ok := d.registry["telegram"].(transport.RichSender)
	if !ok {
		return resp, errors.New("telegram transport cannot send to a thread")
	}

	d.keepMu.Lock()
	defer d.keepMu.Unlock()

	timer, err := telegram.AutoDelete(d.tdjson, d.clientID, chatID)
	if err != nil {
		return resp, err
	}
	// restore puts the timer back, before the pin so the "pinned a message"
	// notice still disappears; the deferred call covers a failed send.
	paused := false
	restore := func() {
		if !paused {
			return
		}
		paused = false
		if err := telegram.SetAutoDelete(d.tdjson, d.clientID, chatID, timer); err != nil {
			fmt.Printf("[ipc] send_keep: restoring the %ds timer in chat %d failed: %v\n", timer, chatID, err)
		}
	}
	defer restore()
	if timer > 0 {
		if err := telegram.SetAutoDelete(d.tdjson, d.clientID, chatID, 0); err != nil {
			return resp, fmt.Errorf("pausing the disappearing timer: %w", err)
		}
		paused = true
	}

	thread, _ := d.sendThread("telegram", req.Thread)
	addr := transport.Address{Transport: "telegram", ID: strconv.FormatInt(chatID, 10)}
	ref, err := rs.SendWith(addr, req.Text, transport.SendOptions{Thread: thread})
	restore()
	if err != nil {
		return resp, err
	}
	resp.MessageID = parseIntOrZero(ref.ID)
	resp.Disappear = timer
	if !req.Pin {
		return resp, nil
	}
	if err := telegram.PinMessage(d.tdjson, d.clientID, chatID, resp.MessageID, req.Silent, false); err != nil {
		return resp, fmt.Errorf("sent but not pinned: %w", err)
	}
	return resp, nil
}
