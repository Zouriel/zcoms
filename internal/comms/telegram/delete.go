package telegram

import (
	"encoding/json"
	"fmt"
	"time"
)

// maxDeleteBatch is how many ids one deleteMessages request carries.
const maxDeleteBatch = 100

// DeleteMessagesRequest is the deleteMessages request. revoke deletes for
// everyone, not just this account.
func DeleteMessagesRequest(chatID int64, ids []int64, revoke bool) map[string]any {
	return map[string]any{
		"@type":       "deleteMessages",
		"chat_id":     chatID,
		"message_ids": ids,
		"revoke":      revoke,
	}
}

// DeleteMessages deletes messages for everyone, in batches. Deleting someone
// else's message in a group needs the delete right there.
func DeleteMessages(tdjson *TDJSON, clientID int32, chatID int64, ids []int64) error {
	for len(ids) > 0 {
		n := min(len(ids), maxDeleteBatch)
		if _, err := request(tdjson, clientID, DeleteMessagesRequest(chatID, ids[:n], true), "delete-msgs", 30*time.Second); err != nil {
			return fmt.Errorf("deleting messages: %w", err)
		}
		ids = ids[n:]
	}
	return nil
}

// MessageByDate returns the id of the last message sent in a chat no later
// than at, or 0 when there is none.
func MessageByDate(tdjson *TDJSON, clientID int32, chatID int64, at time.Time) (int64, error) {
	req := map[string]any{"@type": "getChatMessageByDate", "chat_id": chatID, "date": at.Unix()}
	resp, err := request(tdjson, clientID, req, "msg-by-date", 15*time.Second)
	if err != nil {
		return 0, err
	}
	var out struct {
		Type string `json:"@type"`
		ID   int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, fmt.Errorf("parsing message: %w", err)
	}
	if out.Type != "message" {
		return 0, nil
	}
	return out.ID, nil
}

// LastMessageID returns the id of a chat's newest message, or 0.
func LastMessageID(tdjson *TDJSON, clientID int32, chatID int64) (int64, error) {
	resp, err := request(tdjson, clientID, map[string]any{"@type": "getChat", "chat_id": chatID}, "last-msg", 10*time.Second)
	if err != nil {
		return 0, err
	}
	var out struct {
		Last *struct {
			ID int64 `json:"id"`
		} `json:"last_message"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, fmt.Errorf("parsing chat: %w", err)
	}
	if out.Last == nil {
		return 0, nil
	}
	return out.Last.ID, nil
}

// TopicHistoryPage returns up to limit messages of a forum topic from
// fromMessageID (0 = newest) back, newest first, without opening the chat.
// offset 0 starts just before fromMessageID; -1 includes it.
func TopicHistoryPage(tdjson *TDJSON, clientID int32, chatID, topicID, fromMessageID int64, offset, limit int) ([]Message, error) {
	req := map[string]any{
		"@type": "getForumTopicHistory", "chat_id": chatID, "forum_topic_id": topicID,
		"from_message_id": fromMessageID, "offset": offset, "limit": limit,
	}
	resp, err := request(tdjson, clientID, req, "topic-history", 15*time.Second)
	if err != nil {
		return nil, err
	}
	var out ChatHistory
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return nil, fmt.Errorf("parsing topic history: %w", err)
	}
	return out.Messages, nil
}
