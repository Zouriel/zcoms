package hub

import (
	"errors"
	"net"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
)

// handleMessageOp serves edit, pin, unpin and unpin_all on Telegram: it
// resolves the chat, defaults a zero message id to the newest message this
// account sent there, then acts on it.
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
	if req.Op == "unpin_all" {
		return resp, telegram.UnpinAllMessages(d.tdjson, d.clientID, chatID)
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
