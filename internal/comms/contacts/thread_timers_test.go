package contacts

import (
	"path/filepath"
	"testing"
)

func TestThreadTimers(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "comms.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetThreadTimer("telegram", "-100", "20", 86400, 500); err != nil {
		t.Fatal(err)
	}
	// Changing the timer keeps the sweep mark; advancing never goes back.
	if err := s.SetThreadTimer("telegram", "-100", "20", 3600, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceThreadTimer("telegram", "-100", "20", 400); err != nil {
		t.Fatal(err)
	}
	ts, err := s.ThreadTimers("telegram", "-100")
	if err != nil || len(ts) != 1 || ts[0].Seconds != 3600 || ts[0].SinceID != 500 {
		t.Fatalf("timers = %+v, %v", ts, err)
	}
	if err := s.AdvanceThreadTimer("telegram", "-100", "20", 900); err != nil {
		t.Fatal(err)
	}
	if ts, _ := s.ThreadTimers("telegram", ""); ts[0].SinceID != 900 {
		t.Fatalf("since = %d, want 900", ts[0].SinceID)
	}
	if err := s.SetThreadTimer("telegram", "-100", "20", 0, 0); err != nil {
		t.Fatal(err)
	}
	if ts, _ := s.ThreadTimers("telegram", ""); len(ts) != 0 {
		t.Fatalf("not removed: %+v", ts)
	}
}
