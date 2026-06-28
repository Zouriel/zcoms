package telegram

import (
	"encoding/json"
	"fmt"
	"time"
)

func WaitForUpdateType(tdjson *TDJSON, clientID int32, wantedType string, timeout time.Duration) (string, error) {
	d := dispatcherFor(tdjson)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		select {
		case update := <-d.updates:
			var envelope struct {
				Type string `json:"@type"`
			}
			if err := json.Unmarshal([]byte(update), &envelope); err != nil {
				continue
			}
			if envelope.Type == wantedType {
				return update, nil
			}
		case <-time.After(time.Until(deadline)):
		case <-d.stopCh:
			return "", fmt.Errorf("TDLib client closed")
		}
	}

	return "", fmt.Errorf("timed out waiting for update type %s", wantedType)
}

type NewMessageUpdate struct {
	Type    string `json:"@type"`
	Message struct {
		ID      int64 `json:"id"`
		ChatID  int64 `json:"chat_id"`
		Content struct {
			Type string `json:"@type"`
			Text struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func ReceiveUpdates(tdjson *TDJSON) (string, error) {
	d := dispatcherFor(tdjson)
	select {
	case update := <-d.updates:
		return update, nil
	case <-time.After(5 * time.Second):
		return "", nil
	case <-d.stopCh:
		return "", nil
	}
}

func ParseNewMessage(updateJSON string) (*NewMessageUpdate, bool) {
	var u NewMessageUpdate
	if err := json.Unmarshal([]byte(updateJSON), &u); err != nil {
		return nil, false
	}
	if u.Type != "updateNewMessage" {
		return nil, false
	}
	if u.Message.Content.Type != "messageText" {
		return nil, false
	}
	return &u, true
}

type UpdateNewMessage struct {
	Type    string  `json:"@type"`
	Message Message `json:"message"`
}

type Message struct {
	ID         int64    `json:"id"`
	ChatID     int64    `json:"chat_id"`
	Date       int64    `json:"date"`
	IsOutgoing bool     `json:"is_outgoing"`
	SenderID   SenderID `json:"sender_id"`
	Content    Content  `json:"content"`
}

type SenderID struct {
	Type   string `json:"@type"`
	UserID int64  `json:"user_id,omitempty"`
	ChatID int64  `json:"chat_id,omitempty"`
}

type Content struct {
	Type string `json:"@type"`
	Text struct {
		Text string `json:"text"`
	} `json:"text"`
	Caption struct {
		Text string `json:"text"`
	} `json:"caption"`

	Photo *struct {
		Sizes []PhotoSize `json:"sizes"`
	} `json:"photo"`
	Video *struct {
		FileName string `json:"file_name"`
		Video    File   `json:"video"`
	} `json:"video"`
	Audio *struct {
		FileName string `json:"file_name"`
		Audio    File   `json:"audio"`
	} `json:"audio"`
	Document *struct {
		FileName string `json:"file_name"`
		Document File   `json:"document"`
	} `json:"document"`
	Animation *struct {
		FileName  string `json:"file_name"`
		Animation File   `json:"animation"`
	} `json:"animation"`
	VoiceNote *struct {
		Voice File `json:"voice"`
	} `json:"voice_note"`
	VideoNote *struct {
		Video File `json:"video"`
	} `json:"video_note"`
	Sticker *struct {
		Sticker File `json:"sticker"`
	} `json:"sticker"`
}

func ParseUpdateNewMessage(updateJSON string) (*UpdateNewMessage, bool) {
	var u UpdateNewMessage
	if err := json.Unmarshal([]byte(updateJSON), &u); err != nil {
		return nil, false
	}
	if u.Type != "updateNewMessage" {
		return nil, false
	}
	return &u, true
}

// ParseUpdateAuthorizationState returns the new authorization state from an
// updateAuthorizationState event (false for any other update), so a long-running
// client can react when the session is logged out or closed remotely.
func ParseUpdateAuthorizationState(updateJSON string) (AuthorizationState, bool) {
	var u struct {
		Type               string `json:"@type"`
		AuthorizationState struct {
			Type string `json:"@type"`
		} `json:"authorization_state"`
	}
	if err := json.Unmarshal([]byte(updateJSON), &u); err != nil {
		return AuthStateUnknown, false
	}
	if u.Type != "updateAuthorizationState" {
		return AuthStateUnknown, false
	}
	return AuthorizationState(u.AuthorizationState.Type), true
}
