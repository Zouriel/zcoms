package telegram

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"
)

// GroupChat is a basic group or (non-channel) supergroup the account is in.
type GroupChat struct {
	ChatID int64
	Title  string
}

// chatKind is the part of a getChat reply group code needs.
type chatKind struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Type  struct {
		Type         string `json:"@type"`
		BasicGroupID int64  `json:"basic_group_id"`
		SupergroupID int64  `json:"supergroup_id"`
		IsChannel    bool   `json:"is_channel"`
	} `json:"type"`
}

func (c chatKind) isGroup() bool {
	switch c.Type.Type {
	case "chatTypeBasicGroup":
		return true
	case "chatTypeSupergroup":
		return !c.Type.IsChannel
	}
	return false
}

func fetchChatKind(tdjson *TDJSON, clientID int32, chatID int64) (chatKind, error) {
	resp, err := SendRequestAndWait(tdjson, clientID, fmt.Sprintf(`{"@type":"getChat","chat_id":%d}`, chatID), "get-chat-kind", 10*time.Second)
	if err != nil {
		return chatKind{}, err
	}
	var c chatKind
	err = json.Unmarshal([]byte(resp), &c)
	return c, err
}

// IsGroupChat reports whether chatID is a basic group or a non-channel
// supergroup.
func IsGroupChat(tdjson *TDJSON, clientID int32, chatID int64) (bool, string, error) {
	c, err := fetchChatKind(tdjson, clientID, chatID)
	if err != nil {
		return false, "", err
	}
	return c.isGroup(), c.Title, nil
}

func request(tdjson *TDJSON, clientID int32, req map[string]any, tag string, timeout time.Duration) (string, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	return SendRequestAndWait(tdjson, clientID, string(b), tag, timeout)
}

// CreateSupergroup creates a non-channel supergroup and returns its chat id;
// forum makes it a forum (topics on) from the start. A supergroup keeps one chat id for life, whereas a basic group
// is migrated to a supergroup (and changes id) on several ordinary admin
// actions, which would orphan the registry row.
func CreateSupergroup(tdjson *TDJSON, clientID int32, title string, forum bool) (int64, error) {
	resp, err := request(tdjson, clientID, CreateSupergroupRequest(title, forum), "create-supergroup", 30*time.Second)
	if err != nil {
		return 0, err
	}
	var c chatKind
	if err := json.Unmarshal([]byte(resp), &c); err != nil {
		return 0, err
	}
	if c.ID == 0 {
		return 0, fmt.Errorf("createNewSupergroupChat unexpected response: %s", resp)
	}
	return c.ID, nil
}

// CreateSupergroupRequest is the createNewSupergroupChat request.
func CreateSupergroupRequest(title string, forum bool) map[string]any {
	return map[string]any{
		"@type":                    "createNewSupergroupChat",
		"title":                    title,
		"is_forum":                 forum,
		"is_channel":               false,
		"description":              "",
		"message_auto_delete_time": 0,
		"for_import":               false,
	}
}

// AddChatMembers adds users to a group. Users Telegram refused (usually their
// privacy settings) are returned; the request itself succeeding means the rest
// were added. addChatMembers only works for supergroups, so a basic group is
// filled one user at a time with addChatMember.
func AddChatMembers(tdjson *TDJSON, clientID int32, chatID int64, userIDs []int64) ([]int64, error) {
	if len(userIDs) == 0 {
		return nil, nil
	}
	c, err := fetchChatKind(tdjson, clientID, chatID)
	if err != nil {
		return nil, fmt.Errorf("reading chat %d: %w", chatID, err)
	}
	if c.Type.Type == "chatTypeBasicGroup" {
		return addBasicGroupMembers(tdjson, clientID, chatID, userIDs)
	}
	resp, err := request(tdjson, clientID, map[string]any{
		"@type":    "addChatMembers",
		"chat_id":  chatID,
		"user_ids": userIDs,
	}, "add-chat-members", 30*time.Second)
	if err != nil {
		return nil, err
	}
	return failedToAdd(resp), nil
}

// basicGroupForwardLimit is how many earlier messages a user added to a basic
// group gets to see (Telegram's maximum).
const basicGroupForwardLimit = 100

// addBasicGroupMembers adds users to a basic group one by one. A user whose
// add fails is reported as refused; the rest still get added. When every add
// fails with an error the first error is returned instead, since that usually
// means the account itself cannot add anyone.
func addBasicGroupMembers(tdjson *TDJSON, clientID int32, chatID int64, userIDs []int64) ([]int64, error) {
	var failed []int64
	var firstErr error
	for _, uid := range userIDs {
		resp, err := request(tdjson, clientID, map[string]any{
			"@type":         "addChatMember",
			"chat_id":       chatID,
			"user_id":       uid,
			"forward_limit": basicGroupForwardLimit,
		}, "add-chat-member", 30*time.Second)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			failed = append(failed, uid)
			continue
		}
		failed = append(failed, failedToAdd(resp)...)
	}
	if firstErr != nil && len(failed) == len(userIDs) {
		return nil, firstErr
	}
	return failed, nil
}

// failedToAdd reads the users an add refused. Recent TDLib answers
// failedToAddMembers; older builds answer ok.
func failedToAdd(resp string) []int64 {
	var out struct {
		Failed []struct {
			UserID int64 `json:"user_id"`
		} `json:"failed_to_add_members"`
	}
	if json.Unmarshal([]byte(resp), &out) != nil {
		return nil
	}
	failed := make([]int64, 0, len(out.Failed))
	for _, f := range out.Failed {
		failed = append(failed, f.UserID)
	}
	return failed
}

// FetchGroupChats lists the basic groups and non-channel supergroups in the
// main chat list.
func FetchGroupChats(tdjson *TDJSON, clientID int32) ([]GroupChat, error) {
	// loadChats pulls the list into TDLib's cache; it answers 404 once the whole
	// list is loaded, which is the normal end state.
	for i := 0; i < 20; i++ {
		_, err := request(tdjson, clientID, map[string]any{
			"@type":     "loadChats",
			"chat_list": map[string]any{"@type": "chatListMain"},
			"limit":     500,
		}, "load-chats", 30*time.Second)
		if err != nil {
			break
		}
	}
	resp, err := request(tdjson, clientID, map[string]any{
		"@type":     "getChats",
		"chat_list": map[string]any{"@type": "chatListMain"},
		"limit":     5000,
	}, "get-chats-groups", 30*time.Second)
	if err != nil {
		return nil, err
	}
	var list chatListResponse
	if err := json.Unmarshal([]byte(resp), &list); err != nil {
		return nil, err
	}
	var out []GroupChat
	for _, id := range list.ChatIDs {
		if id >= 0 {
			continue // private and secret chats have positive ids
		}
		c, err := fetchChatKind(tdjson, clientID, id)
		if err != nil || !c.isGroup() {
			continue
		}
		out = append(out, GroupChat{ChatID: c.ID, Title: c.Title})
	}
	return out, nil
}

// FetchGroupMemberIDs returns the user ids of a group's members.
func FetchGroupMemberIDs(tdjson *TDJSON, clientID int32, chatID int64) ([]int64, error) {
	c, err := fetchChatKind(tdjson, clientID, chatID)
	if err != nil {
		return nil, err
	}
	type member struct {
		MemberID SenderID `json:"member_id"`
	}
	collect := func(ms []member, into []int64) []int64 {
		for _, m := range ms {
			if m.MemberID.Type == "messageSenderUser" {
				into = append(into, m.MemberID.UserID)
			}
		}
		return into
	}
	switch {
	case c.Type.Type == "chatTypeBasicGroup":
		resp, err := request(tdjson, clientID, map[string]any{
			"@type": "getBasicGroupFullInfo", "basic_group_id": c.Type.BasicGroupID,
		}, "basic-group-info", 15*time.Second)
		if err != nil {
			return nil, err
		}
		var info struct {
			Members []member `json:"members"`
		}
		if err := json.Unmarshal([]byte(resp), &info); err != nil {
			return nil, err
		}
		return collect(info.Members, nil), nil
	case c.isGroup():
		var ids []int64
		for offset := 0; offset < 10000; {
			resp, err := request(tdjson, clientID, map[string]any{
				"@type":         "getSupergroupMembers",
				"supergroup_id": c.Type.SupergroupID,
				"filter":        map[string]any{"@type": "supergroupMembersFilterRecent"},
				"offset":        offset,
				"limit":         200,
			}, "supergroup-members", 15*time.Second)
			if err != nil {
				return nil, err
			}
			var page struct {
				TotalCount int      `json:"total_count"`
				Members    []member `json:"members"`
			}
			if err := json.Unmarshal([]byte(resp), &page); err != nil {
				return nil, err
			}
			ids = collect(page.Members, ids)
			offset += len(page.Members)
			if len(page.Members) == 0 || offset >= page.TotalCount {
				break
			}
		}
		return ids, nil
	}
	return nil, fmt.Errorf("chat %d is not a group", chatID)
}

// FetchMessage returns one message of a chat.
func FetchMessage(tdjson *TDJSON, clientID int32, chatID, messageID int64) (Message, error) {
	resp, err := request(tdjson, clientID, map[string]any{
		"@type": "getMessage", "chat_id": chatID, "message_id": messageID,
	}, "get-message", 10*time.Second)
	if err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal([]byte(resp), &m); err != nil {
		return Message{}, err
	}
	return m, nil
}

// historyFrom returns up to limit messages starting at fromMessageID shifted by
// offset (a negative offset includes newer messages), newest first.
func historyFrom(tdjson *TDJSON, clientID int32, chatID, fromMessageID int64, offset, limit int) ([]Message, error) {
	resp, err := request(tdjson, clientID, map[string]any{
		"@type":           "getChatHistory",
		"chat_id":         chatID,
		"from_message_id": fromMessageID,
		"offset":          offset,
		"limit":           limit,
		"only_local":      false,
	}, "group-history", 15*time.Second)
	if err != nil {
		return nil, err
	}
	var out ChatHistory
	err = json.Unmarshal([]byte(resp), &out)
	return out.Messages, err
}

// maxGroupWindow bounds a single group read.
const maxGroupWindow = 2000

// FetchOlder returns up to limit messages strictly older than fromMessageID
// (0 = from the latest, inclusive), stopping early at a message dated before
// notBefore (unix seconds, 0 = no bound). Newest first.
func FetchOlder(tdjson *TDJSON, clientID int32, chatID, fromMessageID int64, limit int, notBefore int64) ([]Message, error) {
	if limit <= 0 || limit > maxGroupWindow {
		limit = maxGroupWindow
	}
	var out []Message
	from := fromMessageID
	for tries := 0; len(out) < limit && tries < 200; tries++ {
		want := limit - len(out) + 1
		if want > 100 {
			want = 100
		}
		page, err := historyFrom(tdjson, clientID, chatID, from, 0, want)
		if err != nil {
			return out, err
		}
		progressed := false
		for _, m := range page {
			if fromMessageID != 0 && m.ID >= fromMessageID {
				continue
			}
			if len(out) > 0 && m.ID >= out[len(out)-1].ID {
				continue
			}
			if notBefore > 0 && m.Date < notBefore {
				return out, nil
			}
			out = append(out, m)
			progressed = true
			if len(out) >= limit {
				break
			}
		}
		if !progressed {
			break
		}
		from = out[len(out)-1].ID
	}
	return out, nil
}

// FetchNewer returns up to limit messages strictly newer than fromMessageID,
// oldest first. A single TDLib call can look at most 99 messages ahead, so the
// window pages forward from the newest message seen.
func FetchNewer(tdjson *TDJSON, clientID int32, chatID, fromMessageID int64, limit int) ([]Message, error) {
	var out []Message
	from := fromMessageID
	for tries := 0; len(out) < limit && tries < 50; tries++ {
		ahead := limit - len(out)
		if ahead > 99 {
			ahead = 99
		}
		page, err := historyFrom(tdjson, clientID, chatID, from, -ahead, ahead+1)
		if err != nil {
			return out, err
		}
		var newer []Message
		for i := len(page) - 1; i >= 0; i-- { // page is newest first
			if page[i].ID > from {
				newer = append(newer, page[i])
			}
		}
		if len(newer) == 0 {
			break
		}
		for _, m := range newer {
			if len(out) >= limit {
				break
			}
			out = append(out, m)
		}
		from = out[len(out)-1].ID
	}
	return out, nil
}

// SendOptions are the extras of a group-aware send.
type SendOptions struct {
	ReplyTo  int64
	Mentions []Mention
	// Thread is the forum topic to post in (0 = the chat itself).
	Thread int64
}

// Mention tags a user in an outgoing message. Name is how they are shown;
// when the text already contains Name (or "@"+Username) that occurrence is
// tagged, otherwise Name is appended.
type Mention struct {
	UserID   int64
	Name     string
	Username string
}

// SendTextMessageWith sends text, quoting o.ReplyTo when set and tagging
// o.Mentions. Like SendTextMessage it returns the message's final id once
// Telegram confirms the send.
func SendTextMessageWith(tdjson *TDJSON, clientID int32, chatID int64, text string, o SendOptions) (int64, error) {
	formatted := FormatOutgoingText(tdjson, clientID, text)
	resp, err := request(tdjson, clientID, sendTextRequest(chatID, withMentions(formatted, o.Mentions), o), "send-msg-with", 15*time.Second)
	if err != nil {
		return 0, err
	}
	return confirmSent(tdjson, chatID, resp)
}

// sendTextRequest builds the sendMessage request for formatted text with a
// quote and a forum topic.
func sendTextRequest(chatID int64, formatted FormattedText, o SendOptions) map[string]any {
	req := map[string]any{
		"@type":   "sendMessage",
		"chat_id": chatID,
		"input_message_content": map[string]any{
			"@type": "inputMessageText",
			"text":  formatted,
		},
	}
	if o.ReplyTo != 0 {
		req["reply_to"] = map[string]any{"@type": "inputMessageReplyToMessage", "message_id": o.ReplyTo}
	}
	if o.Thread != 0 {
		req["topic_id"] = topicField(o.Thread)
	}
	return req
}

// withMentions adds a textEntityTypeMentionName entity per mention, over the
// first occurrence of the user's name or @username in the text, appending the
// name when it is absent. Offsets are UTF-16 code units.
func withMentions(ft FormattedText, ms []Mention) FormattedText {
	for _, m := range ms {
		if m.UserID == 0 {
			continue
		}
		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = strings.TrimSpace(m.Username)
		}
		if name == "" {
			continue
		}
		at := -1
		length := 0
		for _, cand := range []string{"@" + strings.TrimPrefix(m.Username, "@"), name} {
			if cand == "@" || cand == "" {
				continue
			}
			if i := strings.Index(ft.Text, cand); i >= 0 {
				at = utf16Len(ft.Text[:i])
				length = utf16Len(cand)
				break
			}
		}
		if at < 0 {
			sep := " "
			if ft.Text == "" || strings.HasSuffix(ft.Text, "\n") {
				sep = ""
			}
			ft.Text += sep
			at = len(utf16.Encode([]rune(ft.Text)))
			ft.Text += name
			length = utf16Len(name)
		}
		ft.Entities = append(ft.Entities, map[string]any{
			"@type":  "textEntity",
			"offset": at,
			"length": length,
			"type":   map[string]any{"@type": "textEntityTypeMentionName", "user_id": m.UserID},
		})
	}
	return ft
}

// MentionsUser reports whether entities in text tag the user: a mention-name
// entity carrying userID, or an @username mention matching username.
func MentionsUser(text string, entities []TextEntity, userID int64, username string) bool {
	username = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(username), "@"))
	units := utf16.Encode([]rune(text))
	for _, e := range entities {
		switch e.Type.Type {
		case "textEntityTypeMentionName":
			if userID != 0 && e.Type.UserID == userID {
				return true
			}
		case "textEntityTypeMention":
			if username == "" || e.Offset < 0 || e.Offset+e.Length > len(units) {
				continue
			}
			got := string(utf16.Decode(units[e.Offset : e.Offset+e.Length]))
			if strings.EqualFold(strings.TrimPrefix(got, "@"), username) {
				return true
			}
		}
	}
	return false
}
