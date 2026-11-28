package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestProjectSayLine(t *testing.T) {
	dir := t.TempDir()
	shot := filepath.Join(dir, "shot one.png")
	if err := os.WriteFile(shot, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := projectSayLine("/repo", []string{"--thread", "login bug", "--file", shot, "fixed", "the", "form"})
	if err != nil {
		t.Fatal(err)
	}
	want := `project say --path /repo --thread "login bug" --file ` + shellQuote(shot) + ` "fixed the form"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// Files alone are enough; text alone keeps the old shape.
	if got, err := projectSayLine("/repo", []string{"--file=" + shot}); err != nil || got != "project say --path /repo --file "+shellQuote(shot) {
		t.Fatalf("file only: %s %v", got, err)
	}
	if got, _ := projectSayLine("/repo", []string{"hi"}); got != "project say --path /repo hi" {
		t.Fatalf("text only: %s", got)
	}
	if _, err := projectSayLine("/repo", nil); err == nil {
		t.Fatal("nothing to say must fail")
	}
	if _, err := projectSayLine("/repo", []string{"--file", filepath.Join(dir, "missing.png"), "x"}); err == nil {
		t.Fatal("a missing file must fail")
	}
	if _, err := projectSayLine("/repo", []string{"--thread"}); err == nil {
		t.Fatal("--thread without a value must fail")
	}
}

func TestProjectSayLinePin(t *testing.T) {
	line, err := projectSayLine("/p", []string{"--pin", "--thread", "Misc", "status: green"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, " --pin=true") || !strings.Contains(line, "status: green") {
		t.Fatalf("line = %q", line)
	}
	if _, err := projectSayLine("/p", []string{"--pin", "--file", "/etc/hostname", "x"}); err == nil {
		t.Fatal("--pin with --file: want an error")
	}
}
