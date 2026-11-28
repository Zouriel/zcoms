package hub

import (
	"testing"

	"github.com/Zouriel/zcoms/internal/comms/telegram"
)

func TestKeepMessage(t *testing.T) {
	cases := []struct {
		m    telegram.Message
		keep bool
	}{
		{telegram.Message{Content: telegram.Content{Type: "messageText"}}, false},
		{telegram.Message{IsPinned: true, Content: telegram.Content{Type: "messageText"}}, true},
		{telegram.Message{Content: telegram.Content{Type: "messageForumTopicCreated"}}, true},
		{telegram.Message{Content: telegram.Content{Type: "messagePinMessage"}}, false}, // pin notices go too
	}
	for _, c := range cases {
		if got := keepMessage(c.m); got != c.keep {
			t.Errorf("keepMessage(%+v) = %v, want %v", c.m, got, c.keep)
		}
	}
}
