package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// FetchUserHandle returns a user's "@username" (the stable handle the agent
// tier keys allow-list, claims, and contacts on), or "" when the user has no
// public username. It tolerates both the legacy `username` field and the newer
// `usernames.active_usernames` shape.
func FetchUserHandle(tdjson *TDJSON, clientID int32, userID int64) (string, error) {
	req := fmt.Sprintf(`{"@type":"getUser","user_id":%d}`, userID)
	resp, err := SendRequestAndWait(tdjson, clientID, req, "get-user-handle", 10*time.Second)
	if err != nil {
		return "", err
	}
	var out struct {
		Username  string `json:"username"`
		Usernames struct {
			ActiveUsernames []string `json:"active_usernames"`
		} `json:"usernames"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return "", err
	}
	handle := strings.TrimSpace(out.Username)
	if handle == "" && len(out.Usernames.ActiveUsernames) > 0 {
		handle = strings.TrimSpace(out.Usernames.ActiveUsernames[0])
	}
	if handle == "" {
		return "", nil
	}
	return "@" + strings.TrimPrefix(handle, "@"), nil
}

func FetchUserDisplayName(tdjson *TDJSON, clientID int32, userID int64) (string, error) {
	req := fmt.Sprintf(`{"@type":"getUser","user_id":%d}`, userID)
	resp, err := SendRequestAndWait(tdjson, clientID, req, "get-user", 10*time.Second)
	if err != nil {
		return "", err
	}

	var out struct {
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return "", err
	}

	name := out.FirstName
	if out.LastName != "" {
		name += " " + out.LastName
	}
	if name == "" && out.Username != "" {
		name = "@" + out.Username
	}
	if name == "" {
		name = fmt.Sprintf("user:%d", userID)
	}
	return name, nil
}

func FetchChatTitle(tdjson *TDJSON, clientID int32, chatID int64) (string, error) {
	req := fmt.Sprintf(`{"@type":"getChat","chat_id":%d}`, chatID)
	resp, err := SendRequestAndWait(tdjson, clientID, req, "get-chat", 10*time.Second)
	if err != nil {
		return "", err
	}

	var out struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return "", err
	}

	if out.Title == "" {
		return fmt.Sprintf("chat:%d", chatID), nil
	}
	return out.Title, nil
}

type resolvedChat struct {
	ID int64 `json:"id"`
}

func ResolveChatIdentifierByUsername(tdjson *TDJSON, clientID int32, username string) (int64, error) {
	username = strings.TrimSpace(strings.TrimPrefix(username, "@"))
	if username == "" {
		return 0, fmt.Errorf("username cannot be empty")
	}

	reqBytes, err := json.Marshal(map[string]any{
		"@type":    "searchPublicChat",
		"username": username,
	})
	if err != nil {
		return 0, err
	}

	resp, err := SendRequestAndWait(tdjson, clientID, string(reqBytes), "resolve-chat", 10*time.Second)
	if err != nil {
		return 0, err
	}

	var c resolvedChat
	if err := json.Unmarshal([]byte(resp), &c); err != nil {
		return 0, err
	}
	if c.ID == 0 {
		return 0, fmt.Errorf("chat not found for @%s", username)
	}

	return c.ID, nil
}
