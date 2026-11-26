package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

// sendConfirmTimeout bounds how long a text send waits for TDLib to swap its
// temporary message id for the server's.
const sendConfirmTimeout = 15 * time.Second

// Bounds on remembered send results nobody was waiting for yet.
const (
	maxSendResults = 512
	sendResultTTL  = 2 * time.Minute
)

// sendKey names one pending outgoing message by chat and temporary id.
type sendKey struct {
	chatID int64
	tempID int64
}

// sendResult is the outcome of a pending send: the final id, or an error.
type sendResult struct {
	id  int64
	err error
	at  time.Time
}

// sendUpdate is the part of updateMessageSendSucceeded/Failed we need. Newer
// TDLib carries an error object; older builds carry error_code/error_message.
type sendUpdate struct {
	Type    string `json:"@type"`
	Message struct {
		ID     int64 `json:"id"`
		ChatID int64 `json:"chat_id"`
	} `json:"message"`
	OldMessageID int64 `json:"old_message_id"`
	Error        struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	ErrorCode    int    `json:"error_code"`
	ErrorMessage string `json:"error_message"`
}

// parseSendUpdate recognises a send outcome update. ok is false for any other
// update.
func parseSendUpdate(raw string) (sendKey, sendResult, bool) {
	var u sendUpdate
	if json.Unmarshal([]byte(raw), &u) != nil {
		return sendKey{}, sendResult{}, false
	}
	key := sendKey{chatID: u.Message.ChatID, tempID: u.OldMessageID}
	switch u.Type {
	case "updateMessageSendSucceeded":
		return key, sendResult{id: u.Message.ID}, true
	case "updateMessageSendFailed":
		msg := u.Error.Message
		if msg == "" {
			msg = u.ErrorMessage
		}
		if msg == "" {
			msg = "unknown error"
		}
		return key, sendResult{err: fmt.Errorf("telegram send failed: %s", msg)}, true
	}
	return sendKey{}, sendResult{}, false
}

// noteSendResult hands a send outcome to its waiter, or remembers it briefly
// when the waiter has not registered yet (the update can be read before the
// sender gets back from its sendMessage reply). Called from the receive loop.
func (d *dispatcher) noteSendResult(key sendKey, res sendResult) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ch, ok := d.sendWaiters[key]; ok {
		delete(d.sendWaiters, key)
		ch <- res // buffered (cap 1)
		return
	}
	now := time.Now()
	res.at = now
	if len(d.sendResults) >= maxSendResults {
		for k, r := range d.sendResults {
			if now.Sub(r.at) > sendResultTTL {
				delete(d.sendResults, k)
			}
		}
		if len(d.sendResults) >= maxSendResults {
			for k := range d.sendResults { // still full: drop an arbitrary one
				delete(d.sendResults, k)
				break
			}
		}
	}
	d.sendResults[key] = res
}

// awaitSend waits for the outcome of the send that TDLib gave tempID.
func (d *dispatcher) awaitSend(key sendKey, timeout time.Duration) (int64, error) {
	d.mu.Lock()
	if res, ok := d.sendResults[key]; ok {
		delete(d.sendResults, key)
		d.mu.Unlock()
		return res.id, res.err
	}
	ch := make(chan sendResult, 1)
	d.sendWaiters[key] = ch
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.sendWaiters, key)
		d.mu.Unlock()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		return res.id, res.err
	case <-timer.C:
		return 0, errSendUnconfirmed
	case <-d.stopCh:
		return 0, errSendUnconfirmed
	}
}

var errSendUnconfirmed = errors.New("send not confirmed")

// sentMessage is the part of a sendMessage reply we need.
type sentMessage struct {
	Type         string          `json:"@type"`
	ID           int64           `json:"id"`
	ChatID       int64           `json:"chat_id"`
	SendingState json.RawMessage `json:"sending_state"`
}

// confirmSent turns a sendMessage reply into the message's final id. A
// pending message waits (bounded) for TDLib's send outcome: success yields
// the server id, failure an error. On timeout the temporary id is returned
// with a log line, so a slow network never loses the send itself.
func confirmSent(tdjson *TDJSON, chatID int64, resp string) (int64, error) {
	var out sentMessage
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, fmt.Errorf("failed to parse sendMessage response: %w; resp=%s", err, resp)
	}
	if out.Type != "message" || out.ID == 0 {
		return 0, fmt.Errorf("sendMessage unexpected response: %s", resp)
	}
	if len(out.SendingState) == 0 || string(out.SendingState) == "null" {
		return out.ID, nil // already final
	}
	if out.ChatID == 0 {
		out.ChatID = chatID
	}
	id, err := dispatcherFor(tdjson).awaitSend(sendKey{chatID: out.ChatID, tempID: out.ID}, sendConfirmTimeout)
	if errors.Is(err, errSendUnconfirmed) {
		log.Printf("[telegram] message %d in chat %d not confirmed within %s: returning its temporary id", out.ID, out.ChatID, sendConfirmTimeout)
		return out.ID, nil
	}
	return id, err
}
