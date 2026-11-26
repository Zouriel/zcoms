package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Forum topics (TDLib 1.8.64 names, checked against the installed library):
// createForumTopic {chat_id, name, is_name_implicit, icon} answers a
// forumTopicInfo; getForumTopics {chat_id, query, offset_date,
// offset_message_id, offset_forum_topic_id, limit} answers forumTopics; a send
// targets a topic with topic_id = messageTopicForum{forum_topic_id};
// toggleSupergroupIsForum {supergroup_id, is_forum, has_forum_tabs} needs the
// owner (creator) of the supergroup. Incoming messages carry topic_id the same
// way. Older builds used message_thread_id, which is read as a fallback.

// ErrTopicsOff is returned when a group cannot hold topics and the account
// cannot turn them on. Callers show it as is.
var ErrTopicsOff = errors.New("topics are off in this group; an admin has to enable them in the group settings")

// MessageTopic is a message's topic_id (messageTopicForum in a forum).
type MessageTopic struct {
	Type          string `json:"@type"`
	ForumTopicID  int64  `json:"forum_topic_id"`
	MessageThread int64  `json:"message_thread_id"`
}

// Thread returns the forum topic a message was posted in, or 0 when it is
// not in a forum topic.
func (m Message) Thread() int64 {
	if m.TopicID != nil && m.TopicID.Type == "messageTopicForum" && m.TopicID.ForumTopicID != 0 {
		return m.TopicID.ForumTopicID
	}
	if m.IsTopicMessage && m.MessageThreadID != 0 {
		return m.MessageThreadID
	}
	return 0
}

// topicField is the sendMessage topic_id value for a forum topic.
func topicField(thread int64) map[string]any {
	return map[string]any{"@type": "messageTopicForum", "forum_topic_id": thread}
}

// ForumTopic is one topic of a forum supergroup.
type ForumTopic struct {
	ID   int64
	Name string
}

// forumTopicInfo is the part of a forumTopicInfo object we read.
type forumTopicInfo struct {
	ForumTopicID    int64  `json:"forum_topic_id"`
	MessageThreadID int64  `json:"message_thread_id"`
	Name            string `json:"name"`
	IsGeneral       bool   `json:"is_general"`
}

func (i forumTopicInfo) id() int64 {
	if i.ForumTopicID != 0 {
		return i.ForumTopicID
	}
	return i.MessageThreadID
}

// CreateForumTopicRequest is the createForumTopic request for a topic name.
func CreateForumTopicRequest(chatID int64, name string) map[string]any {
	return map[string]any{
		"@type":            "createForumTopic",
		"chat_id":          chatID,
		"name":             name,
		"is_name_implicit": false,
		"icon":             map[string]any{"@type": "forumTopicIcon", "color": 0x6FB9F0, "custom_emoji_id": "0"},
	}
}

// GetForumTopicsRequest is the getForumTopics request for the first page of
// a forum's topics.
func GetForumTopicsRequest(chatID int64, limit int) map[string]any {
	return map[string]any{
		"@type":                 "getForumTopics",
		"chat_id":               chatID,
		"query":                 "",
		"offset_date":           0,
		"offset_message_id":     0,
		"offset_forum_topic_id": 0,
		"limit":                 limit,
	}
}

// ToggleForumRequest is the toggleSupergroupIsForum request turning topics on.
func ToggleForumRequest(supergroupID int64) map[string]any {
	return map[string]any{
		"@type":          "toggleSupergroupIsForum",
		"supergroup_id":  supergroupID,
		"is_forum":       true,
		"has_forum_tabs": false,
	}
}

// ChatMemberStatus is the part of a chatMember status topic code needs.
type ChatMemberStatus struct {
	Type   string `json:"@type"`
	Rights struct {
		CanManageTopics bool `json:"can_manage_topics"`
		CanChangeInfo   bool `json:"can_change_info"`
	} `json:"rights"`
}

// CanEnableTopics reports whether a member with this status may turn topics
// on. TDLib documents toggleSupergroupIsForum as requiring owner privileges,
// so only the creator qualifies; an administrator's rights do not.
func CanEnableTopics(s ChatMemberStatus) bool {
	return s.Type == "chatMemberStatusCreator"
}

// ForumState is what topic code needs to know about a chat.
type ForumState struct {
	Supergroup   bool // a non-channel supergroup (basic groups are never upgraded)
	SupergroupID int64
	IsForum      bool
	Self         ChatMemberStatus // the account's own status in the chat
}

// FetchForumState reads whether chatID is a forum supergroup and the
// account's own status there (getChat, getSupergroup, getChatMember).
func FetchForumState(tdjson *TDJSON, clientID int32, chatID, self int64) (ForumState, error) {
	c, err := fetchChatKind(tdjson, clientID, chatID)
	if err != nil {
		return ForumState{}, err
	}
	if c.Type.Type != "chatTypeSupergroup" || c.Type.IsChannel {
		return ForumState{}, nil
	}
	st := ForumState{Supergroup: true, SupergroupID: c.Type.SupergroupID}
	resp, err := request(tdjson, clientID, map[string]any{
		"@type": "getSupergroup", "supergroup_id": c.Type.SupergroupID,
	}, "get-supergroup", 10*time.Second)
	if err != nil {
		return st, err
	}
	var sg struct {
		IsForum bool `json:"is_forum"`
	}
	if err := json.Unmarshal([]byte(resp), &sg); err != nil {
		return st, err
	}
	st.IsForum = sg.IsForum
	resp, err = request(tdjson, clientID, map[string]any{
		"@type":     "getChatMember",
		"chat_id":   chatID,
		"member_id": map[string]any{"@type": "messageSenderUser", "user_id": self},
	}, "get-self-member", 10*time.Second)
	if err != nil {
		return st, err
	}
	var cm struct {
		Status ChatMemberStatus `json:"status"`
	}
	if err := json.Unmarshal([]byte(resp), &cm); err != nil {
		return st, err
	}
	st.Self = cm.Status
	return st, nil
}

// EnsureForum makes sure chatID can hold topics: a forum already is fine; a
// supergroup the account created gets topics turned on; anything else
// (a basic group, or a supergroup someone else owns) returns ErrTopicsOff.
func EnsureForum(tdjson *TDJSON, clientID int32, chatID, self int64) error {
	st, err := FetchForumState(tdjson, clientID, chatID, self)
	if err != nil {
		return err
	}
	if st.IsForum {
		return nil
	}
	if !st.Supergroup || !CanEnableTopics(st.Self) {
		return ErrTopicsOff
	}
	if _, err := request(tdjson, clientID, ToggleForumRequest(st.SupergroupID), "toggle-forum", 30*time.Second); err != nil {
		return fmt.Errorf("turning topics on: %w", err)
	}
	return nil
}

// CreateForumTopic creates a topic in a forum and returns its id.
func CreateForumTopic(tdjson *TDJSON, clientID int32, chatID int64, name string) (ForumTopic, error) {
	resp, err := request(tdjson, clientID, CreateForumTopicRequest(chatID, name), "create-topic", 30*time.Second)
	if err != nil {
		return ForumTopic{}, topicErr(err)
	}
	var info forumTopicInfo
	if err := json.Unmarshal([]byte(resp), &info); err != nil {
		return ForumTopic{}, err
	}
	if info.id() == 0 {
		return ForumTopic{}, fmt.Errorf("createForumTopic unexpected response: %s", resp)
	}
	return ForumTopic{ID: info.id(), Name: info.Name}, nil
}

// ForumTopics lists a forum's topics (the first 100), General first.
func ForumTopics(tdjson *TDJSON, clientID int32, chatID int64) ([]ForumTopic, error) {
	resp, err := request(tdjson, clientID, GetForumTopicsRequest(chatID, 100), "get-topics", 30*time.Second)
	if err != nil {
		return nil, topicErr(err)
	}
	return parseForumTopics(resp)
}

func parseForumTopics(resp string) ([]ForumTopic, error) {
	var out struct {
		Topics []struct {
			Info forumTopicInfo `json:"info"`
		} `json:"topics"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return nil, err
	}
	topics := make([]ForumTopic, 0, len(out.Topics))
	for _, t := range out.Topics {
		topics = append(topics, ForumTopic{ID: t.Info.id(), Name: t.Info.Name})
	}
	return topics, nil
}

// topicErr maps TDLib's "not a forum" answers to ErrTopicsOff.
func topicErr(err error) error {
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "forum") || strings.Contains(s, "chat_not_forum") {
		return fmt.Errorf("%w (%v)", ErrTopicsOff, err)
	}
	return err
}

// FileOptions are the extras of a file send: the message to quote and the
// forum topic to post in.
type FileOptions struct {
	ReplyTo int64
	Thread  int64
}

// SendFileRequest builds the sendMessage request for a local file: photos as
// inputMessagePhoto, other kinds by extension (anything unknown as a
// document), quoting o.ReplyTo and posting in o.Thread when set. The caption
// is passed already formatted.
func SendFileRequest(chatID int64, absPath string, caption *FormattedText, o FileOptions) (map[string]any, string) {
	messageType, fileField, label := classifyOutgoing(absPath)
	content := map[string]any{
		"@type":   messageType,
		fileField: map[string]any{"@type": "inputFileLocal", "path": absPath},
	}
	if caption != nil {
		content["caption"] = *caption
	}
	req := map[string]any{
		"@type":                 "sendMessage",
		"chat_id":               chatID,
		"input_message_content": content,
	}
	if o.Thread != 0 {
		req["topic_id"] = topicField(o.Thread)
	}
	if o.ReplyTo != 0 {
		req["reply_to"] = map[string]any{"@type": "inputMessageReplyToMessage", "message_id": o.ReplyTo}
	}
	return req, label
}

// SendLocalFileMessageWith is SendLocalFileMessage with a quote and a topic.
func SendLocalFileMessageWith(tdjson *TDJSON, clientID int32, chatID int64, path, caption string, o FileOptions) (int64, string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return 0, "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return 0, "", fmt.Errorf("file not found: %s", path)
	}
	if info.IsDir() {
		return 0, "", fmt.Errorf("%s is a directory, not a file", path)
	}
	var capt *FormattedText
	if caption != "" {
		ft := FormatOutgoingText(tdjson, clientID, caption)
		capt = &ft
	}
	req, label := SendFileRequest(chatID, abs, capt, o)
	resp, err := request(tdjson, clientID, req, "send-file-with", 30*time.Second)
	if err != nil {
		return 0, "", err
	}
	var out struct {
		Type string `json:"@type"`
		ID   int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, "", fmt.Errorf("failed to parse sendMessage response: %w; resp=%s", err, resp)
	}
	if out.Type != "message" || out.ID == 0 {
		return 0, "", fmt.Errorf("sendMessage unexpected response: %s", resp)
	}
	return out.ID, label, nil
}
