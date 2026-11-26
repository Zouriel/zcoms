package cmd

import "testing"

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":           "plain",
		"Acme build":      `"Acme build"`,
		`say "hi"`:        `"say \"hi\""`,
		`/home/a b/c\d`:   `"/home/a b/c\\d"`,
		"":                `""`,
		"/home/z/project": "/home/z/project",
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseProjectInitFlags(t *testing.T) {
	o, err := parseProjectInitFlags([]string{"--group", "Acme build", "--follow", "on", "--wa"})
	if err != nil || o.group != "Acme build" || o.follow != "on" || o.transport != "whatsapp" {
		t.Fatalf("got %+v %v", o, err)
	}
	if _, err := parseProjectInitFlags([]string{"--group"}); err == nil {
		t.Fatal("a flag without its value must fail")
	}
	if _, err := parseProjectInitFlags([]string{"--bogus"}); err == nil {
		t.Fatal("an unknown flag must fail")
	}
}

func TestParsePlatform(t *testing.T) {
	for in, want := range map[string]string{"t": "telegram", " Tele ": "telegram", "w": "whatsapp", "WhatsApp": "whatsapp", "x": "", "": ""} {
		if got := parsePlatform(in); got != want {
			t.Errorf("parsePlatform(%q) = %q, want %q", in, got, want)
		}
	}
}
