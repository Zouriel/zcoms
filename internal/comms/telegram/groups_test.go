package telegram

import "testing"

func TestMentionsUser(t *testing.T) {
	ent := func(off, n int, typ string, uid int64) TextEntity {
		var e TextEntity
		e.Offset, e.Length, e.Type.Type, e.Type.UserID = off, n, typ, uid
		return e
	}
	// "👋 @ZAgent hi": the emoji is two UTF-16 units, so the mention is at 3.
	text := "👋 @ZAgent hi"
	if !MentionsUser(text, []TextEntity{ent(3, 7, "textEntityTypeMention", 0)}, 1, "zagent") {
		t.Error("@username mention after an emoji not matched")
	}
	if MentionsUser(text, []TextEntity{ent(3, 7, "textEntityTypeMention", 0)}, 1, "other") {
		t.Error("someone else's @username matched")
	}
	if !MentionsUser("hi Zed", []TextEntity{ent(3, 3, "textEntityTypeMentionName", 1)}, 1, "") {
		t.Error("mention-name entity not matched")
	}
	if MentionsUser("hi @zagent", nil, 1, "zagent") {
		t.Error("plain text without an entity must not count")
	}
}

func TestWithMentions(t *testing.T) {
	ft := withMentions(PlainFormattedText("👋 Ibrahim, your turn"), []Mention{
		{UserID: 7, Name: "Ibrahim"},
		{UserID: 8, Name: "Aisha"},
	})
	if ft.Text != "👋 Ibrahim, your turn Aisha" {
		t.Fatalf("text = %q", ft.Text)
	}
	if len(ft.Entities) != 2 {
		t.Fatalf("entities = %#v", ft.Entities)
	}
	first := ft.Entities[0].(map[string]any)
	if first["offset"] != 3 || first["length"] != 7 {
		t.Fatalf("first entity = %#v", first)
	}
	second := ft.Entities[1].(map[string]any)
	if second["offset"] != 22 || second["length"] != 5 {
		t.Fatalf("second entity = %#v", second)
	}
}
