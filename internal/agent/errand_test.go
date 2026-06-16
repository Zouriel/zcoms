package agent

import (
	"encoding/json"
	"testing"
)

// TestParseErrandStart covers the "[deliver] [go] <target> | <brief>" parser
// shared by the bridge command, the ERRAND triage directive, and the CLI.
func TestParseErrandStart(t *testing.T) {
	cases := map[string]struct {
		ok      bool
		target  string
		brief   string
		deliver bool
		auto    bool
	}{
		"@june | make a portfolio":         {true, "@june", "make a portfolio", false, false},
		"deliver @june | make a CV":        {true, "@june", "make a CV", true, false},
		"go @june | quick ask":             {true, "@june", "quick ask", false, true},
		"deliver go #2 | do the thing":     {true, "#2", "do the thing", true, true},
		"wa:9607988692 | collect bio":      {true, "wa:9607988692", "collect bio", false, false},
		"@june no pipe means error":        {false, "", "", false, false},
		"deliver go | brief but no target": {false, "", "brief but no target", true, true},
	}
	for in, want := range cases {
		spec, err := parseErrandStart(in)
		if want.ok != (err == nil) {
			t.Errorf("%q: ok=%v want %v (err=%v)", in, err == nil, want.ok, err)
			continue
		}
		if !want.ok {
			continue
		}
		if spec.Target != want.target || spec.Brief != want.brief ||
			spec.DeliverToTarget != want.deliver || spec.AutoStart != want.auto {
			t.Errorf("%q -> %+v want target=%q brief=%q deliver=%v auto=%v",
				in, spec, want.target, want.brief, want.deliver, want.auto)
		}
	}
}

// TestErrandDirective checks the content-bearing action-line parser both agents
// share (HANDOFF is matched separately, below).
func TestErrandDirective(t *testing.T) {
	cases := map[string]struct {
		match bool
		kind  string
		rest  string
	}{
		"MSG | Hey June! Question 1 of 5:":                                   {true, "MSG", "Hey June! Question 1 of 5:"},
		"RECORD | name: June\nbio: model":                                    {true, "RECORD", "name: June"}, // single-line match
		"SENDFILE | cv.pdf | your new CV":                                    {true, "SENDFILE", "cv.pdf | your new CV"},
		"DELIVER | collected-info.md":                                        {true, "DELIVER", "collected-info.md"},
		"FLAG | the answers look like a CV but the brief asked for a poster": {true, "FLAG", "the answers look like a CV but the brief asked for a poster"},
		"DONE | CV built and sent":                                           {true, "DONE", "CV built and sent"},
		"just my private planning notes":                                     {false, "", ""},
		"msg | lowercase is not a directive":                                 {false, "", ""},
		"MSG |":                                                              {false, "", ""}, // needs a body
	}
	for line, want := range cases {
		// Match on the first line only (directives are single-line).
		first := line
		if i := indexByteTest(line, '\n'); i >= 0 {
			first = line[:i]
		}
		m := errandDirective.FindStringSubmatch(first)
		if want.match != (m != nil) {
			t.Errorf("%q: match=%v want %v", line, m != nil, want.match)
			continue
		}
		if !want.match {
			continue
		}
		if m[1] != want.kind || m[2] != want.rest {
			t.Errorf("%q -> kind=%q rest=%q want %q/%q", line, m[1], m[2], want.kind, want.rest)
		}
	}
}

// TestErrandHandoff covers the interviewer's handoff signal, with or without a
// trailing summary.
func TestErrandHandoff(t *testing.T) {
	for _, in := range []string{"HANDOFF", "HANDOFF | all 7 questions answered"} {
		if !errandHandoff.MatchString(in) {
			t.Errorf("%q should match handoff", in)
		}
	}
	for _, in := range []string{"HANDOFFS", "please HANDOFF", "MSG | not a handoff"} {
		if errandHandoff.MatchString(in) {
			t.Errorf("%q should NOT match handoff", in)
		}
	}
}

// TestExtractRecordBlock verifies the interviewer's multi-line RECORD content is
// captured in full and a trailing HANDOFF is not folded into the file.
func TestExtractRecordBlock(t *testing.T) {
	turn := "Some private thinking.\nRECORD | name: June\nbio: Maldives-based model\nlocation: Male\nHANDOFF | all done"
	got := extractRecordBlock(turn)
	want := "name: June\nbio: Maldives-based model\nlocation: Male"
	if got != want {
		t.Fatalf("extractRecordBlock:\n got %q\nwant %q", got, want)
	}
	if extractRecordBlock("no record here") != "" {
		t.Fatal("expected empty when no RECORD present")
	}
}

func indexByteTest(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// TestErrandMarkSeen covers inbound dedup and the bound on the seen list.
func TestErrandMarkSeen(t *testing.T) {
	e := &Errand{}
	if !e.markSeen("a") {
		t.Fatal("first sight of 'a' should be fresh")
	}
	if e.markSeen("a") {
		t.Fatal("second sight of 'a' should be a duplicate")
	}
	if !e.markSeen("b") {
		t.Fatal("'b' should be fresh")
	}

	// The dedup list is bounded so a long errand can't grow it without limit.
	for i := 0; i < 600; i++ {
		e.markSeen(string(rune('A'+i%26)) + "-" + itoaTest(i))
	}
	if len(e.SeenMsgIDs) > 500 {
		t.Fatalf("seen list not bounded: %d", len(e.SeenMsgIDs))
	}
}

// TestErrandJSONRoundTrip ensures an errand survives marshal/unmarshal (the
// unexported runtime fields must not interfere with persistence).
func TestErrandJSONRoundTrip(t *testing.T) {
	e := &Errand{
		ID:                 "20260616-180000-0001",
		Status:             ErrandProducing,
		Brief:              "make a 2-page CV",
		InterviewSessionID: "sess-interview",
		WorkerSessionID:    "sess-worker",
		InterviewFile:      "/tmp/x/collected-info.md",
		OwnerChat:          42,
		Source:             "wa",
		TargetName:         "Shanas",
		WAChat:             "9607988692@s.whatsapp.net",
		DeliverToTarget:    true,
		SeenMsgIDs:         []string{"m1", "m2"},
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Errand
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != e.ID || got.Status != e.Status || got.WAChat != e.WAChat ||
		got.DeliverToTarget != e.DeliverToTarget || len(got.SeenMsgIDs) != 2 {
		t.Fatalf("round-trip mismatch: id=%q status=%q wa=%q deliver=%v seen=%d",
			got.ID, got.Status, got.WAChat, got.DeliverToTarget, len(got.SeenMsgIDs))
	}
	if !got.active() {
		t.Fatal("active errand should report active()")
	}
}

// itoaTest is a tiny local int->string to avoid importing strconv just for the
// bound test above.
func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
