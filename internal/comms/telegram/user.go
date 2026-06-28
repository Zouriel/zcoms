package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf16"
)

type User struct {
	ID          int64  `json:"id"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	Username    string `json:"username"`
	PhoneNumber string `json:"phone_number"`
}

type FormattedText struct {
	Type     string `json:"@type"`
	Text     string `json:"text"`
	Entities []any  `json:"entities,omitempty"`
}

func PlainFormattedText(text string) FormattedText {
	return FormattedText{
		Type: "formattedText",
		Text: text,
	}
}

func ParseMarkdownV2Text(tdjson *TDJSON, clientID int32, text string) (FormattedText, error) {
	return ParseMarkdownText(tdjson, clientID, text, 2)
}

func ParseMarkdownV1Text(tdjson *TDJSON, clientID int32, text string) (FormattedText, error) {
	return ParseMarkdownText(tdjson, clientID, text, 1)
}

func ParseMarkdownText(tdjson *TDJSON, clientID int32, text string, version int) (FormattedText, error) {
	req := map[string]any{
		"@type": "parseTextEntities",
		"text":  text,
		"parse_mode": map[string]any{
			"@type":   "textParseModeMarkdown",
			"version": version,
		},
	}

	b, err := json.Marshal(req)
	if err != nil {
		return FormattedText{}, err
	}

	resp, err := SendRequestAndWait(tdjson, clientID, string(b), fmt.Sprintf("parse-markdown-v%d", version), 5*time.Second)
	if err != nil {
		return FormattedText{}, err
	}

	var formatted FormattedText
	if err := json.Unmarshal([]byte(resp), &formatted); err != nil {
		return FormattedText{}, fmt.Errorf("failed to parse formatted text: %w; resp=%s", err, resp)
	}
	if formatted.Type != "formattedText" {
		return FormattedText{}, fmt.Errorf("parseTextEntities unexpected response: %s", resp)
	}

	return formatted, nil
}

func FormatOutgoingText(tdjson *TDJSON, clientID int32, text string) FormattedText {
	formatted, err := ParseOutgoingMarkdownText(tdjson, clientID, text)
	if err == nil {
		return formatted
	}

	fmt.Fprintf(os.Stderr, "zc: Markdown parse failed, trying chunked fallback: %v\n", err)
	return FormatOutgoingTextBestEffort(tdjson, clientID, text)
}

func FormatOutgoingTextBestEffort(tdjson *TDJSON, clientID int32, text string) FormattedText {
	var out FormattedText
	out.Type = "formattedText"
	for _, chunk := range markdownFallbackChunks(text) {
		if chunk == "" {
			continue
		}
		formatted, err := ParseOutgoingMarkdownText(tdjson, clientID, chunk)
		if err == nil {
			appendFormattedText(&out, formatted)
			continue
		}

		if strings.Contains(chunk, "```") {
			appendFormattedText(&out, PlainFormattedText(readableMarkdownFallback(chunk)))
			continue
		}

		for _, line := range strings.SplitAfter(chunk, "\n") {
			if line == "" {
				continue
			}
			formatted, err := ParseOutgoingMarkdownText(tdjson, clientID, line)
			if err == nil {
				appendFormattedText(&out, formatted)
				continue
			}
			appendFormattedText(&out, PlainFormattedText(readableMarkdownFallback(line)))
		}
	}
	return out
}

func ParseOutgoingMarkdownText(tdjson *TDJSON, clientID int32, text string) (FormattedText, error) {
	formatted, v2Err := ParseMarkdownV2Text(tdjson, clientID, text)
	if v2Err == nil {
		return formatted, nil
	}
	formatted, v1Err := ParseMarkdownV1Text(tdjson, clientID, text)
	if v1Err == nil {
		return formatted, nil
	}
	return FormattedText{}, fmt.Errorf("MarkdownV2: %v; Markdown: %v", v2Err, v1Err)
}

func appendFormattedText(out *FormattedText, part FormattedText) {
	baseOffset := utf16Len(out.Text)
	out.Text += part.Text
	for _, entity := range part.Entities {
		out.Entities = append(out.Entities, shiftedTextEntity(entity, baseOffset))
	}
}

func shiftedTextEntity(entity any, offsetDelta int) any {
	m, ok := entity.(map[string]any)
	if !ok {
		return entity
	}
	shifted := make(map[string]any, len(m))
	for k, v := range m {
		shifted[k] = v
	}
	switch offset := shifted["offset"].(type) {
	case float64:
		shifted["offset"] = offset + float64(offsetDelta)
	case int:
		shifted["offset"] = offset + offsetDelta
	}
	return shifted
}

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func markdownFallbackChunks(text string) []string {
	var chunks []string
	for len(text) > 0 {
		i := strings.Index(text, "\n\n")
		if i == -1 {
			chunks = append(chunks, text)
			break
		}
		chunks = append(chunks, text[:i+2])
		text = text[i+2:]
	}
	return chunks
}

func readableMarkdownFallback(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		if text[i] == '\\' && i+1 < len(text) && isMarkdownV2Escapable(text[i+1]) {
			i++
		}
		b.WriteByte(text[i])
	}
	return b.String()
}

func isMarkdownV2Escapable(c byte) bool {
	return strings.ContainsRune("_*[]()~`>#+-=|{}.!", rune(c))
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
	formatted := FormatOutgoingText(tdjson, clientID, text)
	req := map[string]any{
		"@type":   "sendMessage",
		"chat_id": chatID,
		"input_message_content": map[string]any{
			"@type": "inputMessageText",
			"text":  formatted,
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
