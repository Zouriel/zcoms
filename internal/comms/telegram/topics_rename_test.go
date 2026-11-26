package telegram

import "testing"

func TestEditForumTopicRequest(t *testing.T) {
	r := EditForumTopicRequest(-100, 20, "Misc")
	if r["@type"] != "editForumTopic" || r["forum_topic_id"] != int64(20) || r["name"] != "Misc" || r["edit_icon_custom_emoji"] != false {
		t.Fatalf("request = %v", r)
	}
}
