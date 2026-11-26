package contacts

import (
	"testing"
	"time"
)

func TestGroupRegistry(t *testing.T) {
	s := openTmp(t)

	g, err := s.UpsertGroup("telegram", "-1001", "Acme build")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if g.Listen || g.Follow {
		t.Fatalf("a new group must be off: %+v", g)
	}
	if _, err := s.UpsertGroup("whatsapp", "1203@g.us", "Acme build"); err != nil {
		t.Fatalf("upsert wa: %v", err)
	}

	exact, _, err := s.ResolveGroups("telegram", "acme BUILD")
	if err != nil || len(exact) != 1 || exact[0].NativeID != "-1001" {
		t.Fatalf("resolve exact: %+v %v", exact, err)
	}
	_, prefix, _ := s.ResolveGroups("telegram", "acme")
	if len(prefix) != 1 {
		t.Fatalf("resolve prefix: %+v", prefix)
	}

	on := true
	if _, err := s.SetGroupFlags(Agent, "telegram", "-1001", &on, nil); err == nil {
		t.Fatal("the agent must not change group flags")
	}
	g, err = s.SetGroupFlags(Owner, "telegram", "-1001", &on, nil)
	if err != nil || !g.Listen || g.Follow {
		t.Fatalf("set listen: %+v %v", g, err)
	}

	// A rename keeps the flags.
	g, err = s.UpsertGroup("telegram", "-1001", "Acme")
	if err != nil || g.Name != "Acme" || !g.Listen {
		t.Fatalf("rename lost flags: %+v %v", g, err)
	}
}

func TestRecentCalls(t *testing.T) {
	s := openTmp(t)
	if err := s.RecordCall("telegram", "-1001", "42", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordCall("telegram", "-1001", "7", time.Now().Add(-30*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"42": true, "7": false, "99": false} {
		got, err := s.RecentCall("telegram", "-1001", id, 24*time.Hour)
		if err != nil || got != want {
			t.Errorf("call %s: got %v (%v), want %v", id, got, err, want)
		}
	}
	if ok, _ := s.RecentCall("telegram", "-2002", "42", 24*time.Hour); ok {
		t.Error("a call in one group must not open another")
	}
}
