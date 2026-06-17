package tdlib

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type User struct {
	ID          int64  `json:"id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Username    string `json:"username"`
	PhoneNumber string `json:"phone_number"`
}

func FetchCurrentUser(tdjson *TDJSON, clientID int32) (User, error) {
	responseJSON, err := SendRequestAndWait(
		tdjson,
		clientID,
		`{"@type":"getMe"}`,
		"get-me",
		5*time.Second,
	)
	if err != nil {
		return User{}, err
	}

	var envelope struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal([]byte(responseJSON), &envelope); err != nil {
		return User{}, err
	}
	if envelope.Type != "user" {
		return User{}, fmt.Errorf("unexpected response from getMe: %s", responseJSON)
	}

	var user User
	if err := json.Unmarshal([]byte(responseJSON), &user); err != nil {
		return User{}, err
	}

	return user, nil
}

func FetchUser(tdjson *TDJSON, clientID int32, userID int64) (User, error) {
	responseJSON, err := SendRequestAndWait(
		tdjson,
		clientID,
		fmt.Sprintf(`{"@type":"getUser","user_id":%d}`, userID),
		"get-user",
		5*time.Second,
	)
	if err != nil {
		return User{}, err
	}

	var user User
	if err := json.Unmarshal([]byte(responseJSON), &user); err != nil {
		return User{}, err
	}
	if user.ID == 0 {
		return User{}, fmt.Errorf("user not found: %d", userID)
	}
	return user, nil
}

func ResolveUserIdentifierByUsername(tdjson *TDJSON, clientID int32, username string) (int64, error) {
	if strings.HasPrefix(username, "@") {
		username = username[1:]
	}

	req := map[string]any{
		"@type":    "searchPublicChat",
		"username": username,
	}

	b, _ := json.Marshal(req)

	resp, err := SendRequestAndWait(tdjson, clientID, string(b), "resolve-username", 10*time.Second)
	if err != nil {
		return 0, err
	}

	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, err
	}
	if out.ID == 0 {
		return 0, fmt.Errorf("user not found: @%s", username)
	}

	return out.ID, nil
}
func CreatePrivateChat(tdjson *TDJSON, clientID int32, userID int64) (int64, error) {
	req := map[string]any{
		"@type":   "createPrivateChat",
		"user_id": userID,
		"force":   false,
	}

	b, _ := json.Marshal(req)

	resp, err := SendRequestAndWait(tdjson, clientID, string(b), "create-chat", 10*time.Second)
	if err != nil {
		return 0, err
	}

	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, err
	}

	return out.ID, nil
}
func SendTextMessage(tdjson *TDJSON, clientID int32, chatID int64, text string) (int64, error) {
	req := map[string]any{
		"@type":   "sendMessage",
		"chat_id": chatID,
		"input_message_content": map[string]any{
			"@type": "inputMessageText",
			"text": map[string]any{
				"@type": "formattedText",
				"text":  text,
			},
		},
	}

	b, err := json.Marshal(req)
	if err != nil {
		return 0, err
	}

	resp, err := SendRequestAndWait(tdjson, clientID, string(b), "send-msg", 15*time.Second)
	if err != nil {
		return 0, err
	}

	var out struct {
		Type string `json:"@type"`
		ID   int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, fmt.Errorf("failed to parse sendMessage response: %w; resp=%s", err, resp)
	}

	if out.Type != "message" || out.ID == 0 {
		return 0, fmt.Errorf("sendMessage unexpected response: %s", resp)
	}

	return out.ID, nil
}
