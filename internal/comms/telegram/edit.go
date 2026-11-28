package telegram

import (
	"errors"
	"fmt"
	"time"
)

// EditMessageTextRequest is the editMessageText request replacing a text
// message's body with already-formatted text.
func EditMessageTextRequest(chatID, messageID int64, formatted FormattedText) map[string]any {
	return map[string]any{
		"@type":      "editMessageText",
		"chat_id":    chatID,
		"message_id": messageID,
		"input_message_content": map[string]any{
			"@type": "inputMessageText",
			"text":  formatted,
		},
	}
}

// EditMessageText replaces the text of a message this account sent. The text
// goes through the same formatting as a send, so markdown renders the same.
func EditMessageText(tdjson *TDJSON, clientID int32, chatID, messageID int64, text string) error {
	formatted := FormatOutgoingText(tdjson, clientID, text)
	if _, err := request(tdjson, clientID, EditMessageTextRequest(chatID, messageID, formatted), "edit-msg", 15*time.Second); err != nil {
		return fmt.Errorf("editing message %d: %w", messageID, err)
	}
	return nil
}

// PinChatMessageRequest is the pinChatMessage request. silent skips the
// "pinned a message" notification; forSelf pins it only on this side (private
// chats only).
func PinChatMessageRequest(chatID, messageID int64, silent, forSelf bool) map[string]any {
	return map[string]any{
		"@type":                "pinChatMessage",
		"chat_id":              chatID,
		"message_id":           messageID,
		"disable_notification": silent,
		"only_for_self":        forSelf,
	}
}

// PinMessage pins a message in a chat. In groups it needs the pin right.
func PinMessage(tdjson *TDJSON, clientID int32, chatID, messageID int64, silent, forSelf bool) error {
	if _, err := request(tdjson, clientID, PinChatMessageRequest(chatID, messageID, silent, forSelf), "pin-msg", 15*time.Second); err != nil {
		return fmt.Errorf("pinning message %d: %w", messageID, err)
	}
	return nil
}

// UnpinChatMessageRequest is the unpinChatMessage request.
func UnpinChatMessageRequest(chatID, messageID int64) map[string]any {
	return map[string]any{
		"@type":      "unpinChatMessage",
		"chat_id":    chatID,
		"message_id": messageID,
	}
}

// UnpinMessage unpins one message in a chat.
func UnpinMessage(tdjson *TDJSON, clientID int32, chatID, messageID int64) error {
	if _, err := request(tdjson, clientID, UnpinChatMessageRequest(chatID, messageID), "unpin-msg", 15*time.Second); err != nil {
		return fmt.Errorf("unpinning message %d: %w", messageID, err)
	}
	return nil
}

// UnpinAllMessages unpins every pinned message in a chat.
func UnpinAllMessages(tdjson *TDJSON, clientID int32, chatID int64) error {
	req := map[string]any{"@type": "unpinAllChatMessages", "chat_id": chatID}
	if _, err := request(tdjson, clientID, req, "unpin-all", 15*time.Second); err != nil {
		return fmt.Errorf("unpinning all messages: %w", err)
	}
	return nil
}

// lastOutgoingScan bounds how far back LastOutgoingID looks.
const lastOutgoingScan = 50

// ErrNoOutgoing is returned when a chat has no recent message from this account.
var ErrNoOutgoing = errors.New("no message from you in the last 50 of this chat")

// LastOutgoingID returns the id of the newest message this account sent in
// the chat, without opening it or marking anything read. It pages until it
// has looked at lastOutgoingScan messages or reached the start of the chat:
// TDLib often answers the first page with only what it has cached (one
// message after a restart), so a short page does not mean the end.
func LastOutgoingID(tdjson *TDJSON, clientID int32, chatID int64) (int64, error) {
	var from int64 // 0 = newest
	seen := 0
	for page := 0; page < 10 && seen < lastOutgoingScan; page++ {
		msgs, err := historyPage(tdjson, clientID, chatID, from, lastOutgoingScan)
		if err != nil {
			return 0, err
		}
		if len(msgs) == 0 || msgs[len(msgs)-1].ID == from {
			break // start of the chat
		}
		if id, err := newestOutgoing(msgs); err == nil {
			return id, nil
		}
		seen += len(msgs)
		from = msgs[len(msgs)-1].ID
	}
	return 0, ErrNoOutgoing
}

// sentContent is the content a person sends. Everything else (a pin notice,
// a topic edit, a join) is a service message, which "last" must skip: pinning
// a message itself adds an outgoing "pinned a message" notice.
var sentContent = map[string]bool{
	"messageText": true, "messagePhoto": true, "messageVideo": true,
	"messageDocument": true, "messageAudio": true, "messageAnimation": true,
	"messageVoiceNote": true, "messageVideoNote": true, "messageSticker": true,
	"messageLocation": true, "messageContact": true, "messagePoll": true,
}

// newestOutgoing picks the first outgoing, non-service message of a
// newest-first history.
func newestOutgoing(msgs []Message) (int64, error) {
	for _, m := range msgs {
		if m.IsOutgoing && sentContent[m.Content.Type] {
			return m.ID, nil
		}
	}
	return 0, ErrNoOutgoing
}
