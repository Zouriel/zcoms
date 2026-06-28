package telegram

import (
	"encoding/json"
	"fmt"
	"time"
)

type chatListResponse struct {
	ChatIDs []int64 `json:"chat_ids"`
}

type chatOrderInfo struct {
	ID    int64 `json:"id"`
	Order int64 `json:"order"`
}

func FetchChatIdentifiers(tdjson *TDJSON, clientID int32, limit int) ([]int64, error) {
	const pageSize = 100

	var out []int64
	offsetOrder := int64(1<<63 - 1)
	offsetChatID := int64(0)

	for {
		want := pageSize
		if limit > 0 {
			remaining := limit - len(out)
			if remaining <= 0 {
				break
			}
			if remaining < want {
				want = remaining
			}
		}

		req := fmt.Sprintf(`{
			"@type": "getChats",
			"chat_list": {"@type":"chatListMain"},
			"offset_order": %d,
			"offset_chat_id": %d,
			"limit": %d
		}`, offsetOrder, offsetChatID, want)

		resp, err := SendRequestAndWait(tdjson, clientID, req, "get-chats", 15*time.Second)
		if err != nil {
			return nil, err
		}

		var r chatListResponse
		if err := json.Unmarshal([]byte(resp), &r); err != nil {
			return nil, err
		}

		if len(r.ChatIDs) == 0 {
			break
		}

		out = append(out, r.ChatIDs...)

		lastID := r.ChatIDs[len(r.ChatIDs)-1]
		chatJSON, err := SendRequestAndWait(tdjson, clientID,
			fmt.Sprintf(`{"@type":"getChat","chat_id":%d}`, lastID),
			"get-chat-order",
			10*time.Second,
		)
		if err != nil {
			return nil, err
		}

		var c chatOrderInfo
		if err := json.Unmarshal([]byte(chatJSON), &c); err != nil {
			return nil, err
		}

		offsetOrder = c.Order
		offsetChatID = c.ID
	}

	return out, nil
}
