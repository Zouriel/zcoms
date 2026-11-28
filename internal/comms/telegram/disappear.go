package telegram

import (
	"encoding/json"
	"fmt"
	"time"
)

// SetAutoDeleteRequest is the setChatMessageAutoDeleteTime request. seconds is
// the disappearing-messages timer for messages sent from now on; 0 turns it
// off. Telegram has one timer per chat: forum topics share the group's.
func SetAutoDeleteRequest(chatID int64, seconds int32) map[string]any {
	return map[string]any{
		"@type":                    "setChatMessageAutoDeleteTime",
		"chat_id":                  chatID,
		"message_auto_delete_time": seconds,
	}
}

// SetAutoDelete sets a chat's disappearing-messages timer. It only affects
// messages sent afterwards: earlier messages keep the timer they were sent with.
func SetAutoDelete(tdjson *TDJSON, clientID int32, chatID int64, seconds int32) error {
	if _, err := request(tdjson, clientID, SetAutoDeleteRequest(chatID, seconds), "set-autodelete", 15*time.Second); err != nil {
		return fmt.Errorf("setting disappearing messages: %w", err)
	}
	return nil
}

// AutoDelete returns a chat's disappearing-messages timer in seconds (0 = off).
func AutoDelete(tdjson *TDJSON, clientID int32, chatID int64) (int32, error) {
	resp, err := request(tdjson, clientID, map[string]any{"@type": "getChat", "chat_id": chatID}, "get-autodelete", 10*time.Second)
	if err != nil {
		return 0, err
	}
	var out struct {
		Seconds int32 `json:"message_auto_delete_time"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, fmt.Errorf("parsing chat: %w", err)
	}
	return out.Seconds, nil
}
