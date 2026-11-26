package telegram

import (
	"errors"
	"testing"
	"time"
)

func testDispatcher() *dispatcher {
	return &dispatcher{
		pending:     map[string]chan string{},
		sendWaiters: map[sendKey]chan sendResult{},
		sendResults: map[sendKey]sendResult{},
		stopCh:      make(chan struct{}),
	}
}

func TestParseSendUpdate(t *testing.T) {
	key, res, ok := parseSendUpdate(`{"@type":"updateMessageSendSucceeded","message":{"id":1048576,"chat_id":-100123},"old_message_id":5}`)
	if !ok || key != (sendKey{chatID: -100123, tempID: 5}) || res.id != 1048576 || res.err != nil {
		t.Fatalf("succeeded: %+v %+v %v", key, res, ok)
	}
	_, res, ok = parseSendUpdate(`{"@type":"updateMessageSendFailed","message":{"id":6,"chat_id":1},"old_message_id":6,"error":{"code":400,"message":"CHAT_WRITE_FORBIDDEN"}}`)
	if !ok || res.err == nil || res.id != 0 {
		t.Fatalf("failed: %+v %v", res, ok)
	}
	_, res, ok = parseSendUpdate(`{"@type":"updateMessageSendFailed","message":{"id":6,"chat_id":1},"old_message_id":6,"error_code":400,"error_message":"OLD"}`)
	if !ok || res.err == nil || res.err.Error() != "telegram send failed: OLD" {
		t.Fatalf("old-style failed: %+v %v", res, ok)
	}
	if _, _, ok := parseSendUpdate(`{"@type":"updateNewMessage"}`); ok {
		t.Fatal("other updates must not parse as send outcomes")
	}
}

// The outcome may be read before the sender registers to wait for it.
func TestSendResultBeforeWaiter(t *testing.T) {
	d := testDispatcher()
	key := sendKey{chatID: 1, tempID: 5}
	d.noteSendResult(key, sendResult{id: 99})
	id, err := d.awaitSend(key, time.Second)
	if err != nil || id != 99 {
		t.Fatalf("got (%d, %v)", id, err)
	}
	if len(d.sendResults) != 0 {
		t.Fatal("a claimed result must be forgotten")
	}
}

func TestSendResultAfterWaiter(t *testing.T) {
	d := testDispatcher()
	key := sendKey{chatID: 1, tempID: 5}
	done := make(chan struct{})
	var id int64
	var err error
	go func() {
		id, err = d.awaitSend(key, 5*time.Second)
		close(done)
	}()
	for {
		d.mu.Lock()
		_, waiting := d.sendWaiters[key]
		d.mu.Unlock()
		if waiting {
			break
		}
		time.Sleep(time.Millisecond)
	}
	d.noteSendResult(key, sendResult{err: errors.New("boom")})
	<-done
	if err == nil || id != 0 {
		t.Fatalf("failure not delivered: (%d, %v)", id, err)
	}
}

func TestSendWaitTimesOut(t *testing.T) {
	d := testDispatcher()
	_, err := d.awaitSend(sendKey{chatID: 1, tempID: 7}, 10*time.Millisecond)
	if !errors.Is(err, errSendUnconfirmed) {
		t.Fatalf("got %v", err)
	}
	if len(d.sendWaiters) != 0 {
		t.Fatal("a timed-out waiter must be removed")
	}
}

func TestSendResultsAreBounded(t *testing.T) {
	d := testDispatcher()
	for i := 0; i < maxSendResults+50; i++ {
		d.noteSendResult(sendKey{chatID: 1, tempID: int64(i)}, sendResult{id: int64(i)})
	}
	if len(d.sendResults) > maxSendResults {
		t.Fatalf("kept %d results", len(d.sendResults))
	}
}

func TestFailedToAdd(t *testing.T) {
	got := failedToAdd(`{"@type":"failedToAddMembers","failed_to_add_members":[{"user_id":7},{"user_id":9}]}`)
	if len(got) != 2 || got[0] != 7 || got[1] != 9 {
		t.Fatalf("got %v", got)
	}
	if got := failedToAdd(`{"@type":"ok"}`); len(got) != 0 {
		t.Fatalf("ok answer: %v", got)
	}
}
