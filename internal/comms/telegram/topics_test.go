package telegram

import (
	"encoding/json"
	"testing"
)

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTopicRequestsJSON(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want string
	}{
		{"create", CreateForumTopicRequest(-1001, "login bug"),
			`{"@type":"createForumTopic","chat_id":-1001,"icon":{"@type":"forumTopicIcon","color":7322096,"custom_emoji_id":"0"},"is_name_implicit":false,"name":"login bug"}`},
		{"list", GetForumTopicsRequest(-1001, 100),
			`{"@type":"getForumTopics","chat_id":-1001,"limit":100,"offset_date":0,"offset_forum_topic_id":0,"offset_message_id":0,"query":""}`},
		{"toggle", ToggleForumRequest(77),
			`{"@type":"toggleSupergroupIsForum","has_forum_tabs":false,"is_forum":true,"supergroup_id":77}`},
		{"supergroup forum", CreateSupergroupRequest("Acme", true),
			`{"@type":"createNewSupergroupChat","description":"","for_import":false,"is_channel":false,"is_forum":true,"message_auto_delete_time":0,"title":"Acme"}`},
	}
	for _, c := range cases {
		if got := jsonOf(t, c.got); got != c.want {
			t.Errorf("%s:\n got  %s\n want %s", c.name, got, c.want)
		}
	}
}

func TestSendRequestsCarryThread(t *testing.T) {
	req := sendTextRequest(-1001, PlainFormattedText("hi"), SendOptions{ReplyTo: 42, Thread: 9})
	got := jsonOf(t, req)
	want := `{"@type":"sendMessage","chat_id":-1001,"input_message_content":{"@type":"inputMessageText","text":{"@type":"formattedText","text":"hi"}},` +
		`"reply_to":{"@type":"inputMessageReplyToMessage","message_id":42},"topic_id":{"@type":"messageTopicForum","forum_topic_id":9}}`
	if got != want {
		t.Fatalf("text:\n got  %s\n want %s", got, want)
	}
	if _, ok := sendTextRequest(-1001, PlainFormattedText("hi"), SendOptions{})["topic_id"]; ok {
		t.Fatal("no thread must leave topic_id out")
	}

	photo, label := SendFileRequest(-1001, "/tmp/shot.png", nil, FileOptions{ReplyTo: 42, Thread: 9})
	if label != "photo" {
		t.Fatalf("png label = %q", label)
	}
	got = jsonOf(t, photo)
	want = `{"@type":"sendMessage","chat_id":-1001,"input_message_content":{"@type":"inputMessagePhoto","photo":{"@type":"inputFileLocal","path":"/tmp/shot.png"}},` +
		`"reply_to":{"@type":"inputMessageReplyToMessage","message_id":42},"topic_id":{"@type":"messageTopicForum","forum_topic_id":9}}`
	if got != want {
		t.Fatalf("photo:\n got  %s\n want %s", got, want)
	}
	doc, label := SendFileRequest(-1001, "/tmp/report.pdf", nil, FileOptions{})
	if label != "document" || doc["input_message_content"].(map[string]any)["@type"] != "inputMessageDocument" {
		t.Fatalf("pdf: %v %s", doc, label)
	}
}

func TestCanEnableTopicsOnlyForCreator(t *testing.T) {
	var st ChatMemberStatus
	for raw, want := range map[string]bool{
		`{"@type":"chatMemberStatusCreator","is_member":true}`:                                                 true,
		`{"@type":"chatMemberStatusAdministrator","rights":{"can_manage_topics":true,"can_change_info":true}}`: false,
		`{"@type":"chatMemberStatusMember"}`:                                                                   false,
	} {
		st = ChatMemberStatus{}
		if err := json.Unmarshal([]byte(raw), &st); err != nil {
			t.Fatal(err)
		}
		if got := CanEnableTopics(st); got != want {
			t.Errorf("%s: got %v, want %v", raw, got, want)
		}
	}
}

func TestMessageThreadAndTopicList(t *testing.T) {
	var m Message
	if err := json.Unmarshal([]byte(`{"id":5,"topic_id":{"@type":"messageTopicForum","forum_topic_id":12}}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Thread() != 12 {
		t.Fatalf("thread = %d", m.Thread())
	}
	m = Message{}
	_ = json.Unmarshal([]byte(`{"id":5,"is_topic_message":true,"message_thread_id":34}`), &m)
	if m.Thread() != 34 {
		t.Fatalf("legacy thread = %d", m.Thread())
	}
	m = Message{}
	_ = json.Unmarshal([]byte(`{"id":5,"message_thread_id":34}`), &m)
	if m.Thread() != 0 {
		t.Fatal("a reply thread outside a forum is not a topic")
	}

	ts, err := parseForumTopics(`{"@type":"forumTopics","total_count":2,"topics":[` +
		`{"info":{"forum_topic_id":1,"name":"General","is_general":true}},{"info":{"forum_topic_id":12,"name":"login bug"}}]}`)
	if err != nil || len(ts) != 2 || ts[1].ID != 12 || ts[1].Name != "login bug" {
		t.Fatalf("topics = %+v %v", ts, err)
	}
}
