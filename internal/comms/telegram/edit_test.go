package telegram

import (
	"errors"
	"testing"
)

func TestEditMessageTextRequest(t *testing.T) {
	r := EditMessageTextRequest(-100, 42, FormattedText{Type: "formattedText", Text: "hi"})
	content, _ := r["input_message_content"].(map[string]any)
	if r["@type"] != "editMessageText" || r["chat_id"] != int64(-100) || r["message_id"] != int64(42) || content["@type"] != "inputMessageText" {
		t.Fatalf("request = %v", r)
	}
}

func TestPinRequests(t *testing.T) {
	p := PinChatMessageRequest(5, 7, true, false)
	if p["@type"] != "pinChatMessage" || p["message_id"] != int64(7) || p["disable_notification"] != true || p["only_for_self"] != false {
		t.Fatalf("pin = %v", p)
	}
	u := UnpinChatMessageRequest(5, 7)
	if u["@type"] != "unpinChatMessage" || u["message_id"] != int64(7) {
		t.Fatalf("unpin = %v", u)
	}
}

func TestNewestOutgoing(t *testing.T) {
	text := Content{Type: "messageText"}
	msgs := []Message{
		{ID: 40, IsOutgoing: true, Content: Content{Type: "messagePinMessage"}}, // service notice
		{ID: 30, Content: text},
		{ID: 20, IsOutgoing: true, Content: text},
		{ID: 10, IsOutgoing: true, Content: text},
	}
	if id, err := newestOutgoing(msgs); err != nil || id != 20 {
		t.Fatalf("got %d, %v; want 20", id, err)
	}
	if _, err := newestOutgoing([]Message{{ID: 1}}); !errors.Is(err, ErrNoOutgoing) {
		t.Fatalf("err = %v; want ErrNoOutgoing", err)
	}
}
