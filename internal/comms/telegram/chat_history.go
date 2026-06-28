package telegram

import (
	"encoding/json"
	"fmt"
	"time"
)

type ChatHistory struct {
	Messages []Message `json:"messages"`
}

func OpenChat(tdjson *TDJSON, clientID int32, chatID int64) error {
	req := fmt.Sprintf(`{
		"@type": "openChat",
		"chat_id": %d
	}`, chatID)

	_, err := SendRequestAndWait(tdjson, clientID, req, "open-chat", 10*time.Second)
	return err
}

// FetchChatHistorySnapshot returns up to limit recent messages (newest first)
// WITHOUT opening the chat or marking anything read — for read-only peeks such as
// `zc tg chat --read` and the daemon's read op / READ directive. It pages via the
// same no-open primitive triage uses (historyPage). Prefer this over
// FetchChatHistory whenever the caller must not emit read receipts.
// maxSnapshotMessages is a hard ceiling so no single snapshot can page an entire
// (possibly huge) conversation into memory, regardless of the requested limit.
const maxSnapshotMessages = 1000

func FetchChatHistorySnapshot(tdjson *TDJSON, clientID int32, chatID int64, limit int) ([]Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	if limit > maxSnapshotMessages {
		limit = maxSnapshotMessages
	}
	collected := make([]Message, 0, limit)
	var from int64 // 0 = newest
	for len(collected) < limit {
		batch := limit - len(collected)
		if batch > 50 {
			batch = 50
		}
		msgs, err := historyPage(tdjson, clientID, chatID, from, batch)
		if err != nil {
			return collected, err
		}
		if len(msgs) == 0 {
			break
		}
		collected = append(collected, msgs...)
		from = msgs[len(msgs)-1].ID // oldest in this page -> next page is older
		if len(msgs) < batch {
			break
		}
	}
	return collected, nil
}

func FetchChatHistory(tdjson *TDJSON, clientID int32, chatID int64, limit int) ([]Message, error) {
	if err := OpenChat(tdjson, clientID, chatID); err != nil {
		return nil, err
	}

	time.Sleep(250 * time.Millisecond)

	if limit <= 0 {
		return nil, nil
	}

	minTarget := 10
	if limit < minTarget {
		minTarget = limit
	}

	var last []Message
	for attempt := 0; attempt < 6; attempt++ {
		collected, err := fetchChatHistory(tdjson, clientID, chatID, limit)
		if err != nil {
			return nil, err
		}
		last = collected
		if len(collected) >= minTarget {
			return collected, nil
		}
		time.Sleep(400 * time.Millisecond)
	}

	return last, nil
}

func fetchChatHistory(tdjson *TDJSON, clientID int32, chatID int64, limit int) ([]Message, error) {
	collected := make([]Message, 0, limit)
	var fromMessageID int64

	for len(collected) < limit {
		batchLimit := limit - len(collected)
		if batchLimit > 50 {
			batchLimit = 50
		}

		offset := 0
		if fromMessageID != 0 {
			offset = -1
		}

		req := fmt.Sprintf(`{
			"@type": "getChatHistory",
			"chat_id": %d,
			"from_message_id": %d,
			"offset": %d,
			"limit": %d,
			"only_local": false
		}`, chatID, fromMessageID, offset, batchLimit)

		resp, err := SendRequestAndWait(tdjson, clientID, req, "get-history", 10*time.Second)
		if err != nil {
			return nil, err
		}

		var out ChatHistory
		if err := json.Unmarshal([]byte(resp), &out); err != nil {
			return nil, err
		}
		if len(out.Messages) == 0 {
			break
		}

		collected = append(collected, out.Messages...)
		fromMessageID = out.Messages[len(out.Messages)-1].ID

		if len(out.Messages) < batchLimit {
			break
		}
	}

	return collected, nil
}
