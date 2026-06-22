package tdlib

import "testing"

func TestReadableMarkdownFallbackUnescapesMarkdownV2(t *testing.T) {
	input := `\*bold\* \[link\]\(https://example.com\) keep\!`
	want := `*bold* [link](https://example.com) keep!`

	if got := readableMarkdownFallback(input); got != want {
		t.Fatalf("readableMarkdownFallback() = %q, want %q", got, want)
	}
}

func TestAppendFormattedTextShiftsEntityOffsets(t *testing.T) {
	out := PlainFormattedText("Hi ")
	part := FormattedText{
		Type: "formattedText",
		Text: "there",
		Entities: []any{
			map[string]any{
				"@type":  "textEntity",
				"offset": float64(0),
				"length": float64(5),
				"type": map[string]any{
					"@type": "textEntityTypeBold",
				},
			},
		},
	}

	appendFormattedText(&out, part)

	if out.Text != "Hi there" {
		t.Fatalf("text = %q, want %q", out.Text, "Hi there")
	}
	entity := out.Entities[0].(map[string]any)
	if got := entity["offset"]; got != float64(3) {
		t.Fatalf("offset = %v, want 3", got)
	}
}
