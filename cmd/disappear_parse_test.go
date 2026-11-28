package cmd

import (
	"testing"
	"time"
)

func TestParseDisappear(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		in   string
		want time.Duration
		bad  bool
	}{
		{in: "off"}, {in: "0"},
		{in: "1d", want: day}, {in: "7D", want: 7 * day}, {in: "2w", want: 14 * day},
		{in: "12h", want: 12 * time.Hour},
		{in: "-1d", bad: true}, {in: "xd", bad: true}, {in: "soon", bad: true},
	}
	for _, tt := range tests {
		got, err := parseDisappear(tt.in)
		if (err != nil) != tt.bad || got != tt.want {
			t.Errorf("parseDisappear(%q) = %v, %v; want %v, bad=%v", tt.in, got, err, tt.want, tt.bad)
		}
	}
	if s := formatDisappear(14 * day); s != "2w" {
		t.Errorf("formatDisappear(14d) = %q", s)
	}
	if s := formatDisappear(3 * day); s != "3d" {
		t.Errorf("formatDisappear(3d) = %q", s)
	}
}
