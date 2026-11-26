package whatsapp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/transport"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

var mentionMe = &waE2E.ContextInfo{MentionedJID: []string{"9607000000@s.whatsapp.net"}}

// fakeDownloads counts media fetches and answers with fixed bytes.
func fakeDownloads(tr *Transport) *int {
	n := 0
	tr.download = func(m *waE2E.Message) ([]byte, error) {
		n++
		if m == nil {
			return nil, errors.New("nil message")
		}
		return []byte("img"), nil
	}
	return &n
}

func image(caption string, size uint64, ci *waE2E.ContextInfo) *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
		Caption: proto.String(caption), Mimetype: proto.String("image/jpeg"),
		FileLength: proto.Uint64(size), ContextInfo: ci,
	}}
}

func TestWhatsAppNonCallMediaIsNotDownloaded(t *testing.T) {
	tr, ch := storeTransport(t, true, true)
	n := fakeDownloads(tr)
	m := groupMessage(image("holiday", 10, nil))
	m.Info.Timestamp = time.Now() // inside the follow group's 30 days
	tr.onMessage(m)
	if in, ok := received(ch); ok {
		t.Fatalf("non-call produced %+v", in)
	}
	if *n != 0 {
		t.Fatalf("non-call media downloaded %d times", *n)
	}
	var kind, text string
	if err := tr.db.QueryRow(`SELECT kind, text FROM zc_messages WHERE chat_jid=?`, testGroup).Scan(&kind, &text); err != nil {
		t.Fatal(err)
	}
	if kind != "messageImage" {
		t.Fatalf("stored kind %q", kind)
	}
}

func TestWhatsAppCallMediaIsDownloaded(t *testing.T) {
	tr, ch := storeTransport(t, true, false)
	n := fakeDownloads(tr)
	m := groupMessage(image("@9607000000 what is this", 10, mentionMe))
	m.Info.ID = "3EB0PHOTO"
	tr.onMessage(m)
	in, ok := received(ch)
	if !ok || in.Call != transport.CallMention {
		t.Fatalf("no call: %+v", in)
	}
	if *n != 1 || len(in.Files) != 1 {
		t.Fatalf("call media: downloads %d files %v", *n, in.Files)
	}
	want := filepath.Join(filepath.Dir(tr.dbPath), "whatsmeow-media", "groups", "120363000000000001", "3EB0PHOTO.jpg")
	if in.Files[0] != want {
		t.Fatalf("saved at %s, want %s", in.Files[0], want)
	}
	if b, err := os.ReadFile(want); err != nil || string(b) != "img" {
		t.Fatalf("saved file: %q %v", b, err)
	}
}

func TestWhatsAppQuotedMediaIsDownloaded(t *testing.T) {
	tr, ch := storeTransport(t, true, false)
	n := fakeDownloads(tr)
	ci := &waE2E.ContextInfo{
		MentionedJID:  []string{"9607000000@s.whatsapp.net"},
		StanzaID:      proto.String("3EB0QUOTED"),
		Participant:   proto.String("9607000002@s.whatsapp.net"),
		QuotedMessage: &waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{FileName: proto.String("plan.pdf"), FileLength: proto.Uint64(10)}},
	}
	tr.onMessage(groupMessage(text("@9607000000 summarise", ci)))
	in, ok := received(ch)
	if !ok {
		t.Fatal("no call")
	}
	if len(in.Files) != 0 || *n != 1 || !strings.HasSuffix(in.ReplyFile, "3EB0QUOTED-plan.pdf") {
		t.Fatalf("quoted media: files %v reply %q downloads %d", in.Files, in.ReplyFile, *n)
	}
}

func TestWhatsAppCallMediaOverCapIsSkipped(t *testing.T) {
	tr, ch := storeTransport(t, true, false)
	n := fakeDownloads(tr)
	tr.onMessage(groupMessage(image("@9607000000 look", maxGroupMediaBytes+1, mentionMe)))
	in, ok := received(ch)
	if !ok {
		t.Fatal("no call")
	}
	if *n != 0 || len(in.Files) != 0 || !strings.Contains(in.Text, "larger than 20 MB") {
		t.Fatalf("oversized: downloads %d files %v text %q", *n, in.Files, in.Text)
	}
}

func TestTrimGroupStoreFollowsFlags(t *testing.T) {
	tr, _ := storeTransport(t, true, true)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 230; i++ {
		tr.storeGroupMessage(testGroup, "m"+time.Duration(i).String(), "x", "1@s.whatsapp.net", false, "t", "messageText",
			base.Add(time.Duration(i)*time.Second), "", true)
	}
	if n := countRows(t, tr); n != 230 {
		t.Fatalf("follow kept %d", n)
	}
	if err := tr.TrimGroupStore(testGroup, transport.GroupFlags{Listen: true}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, tr); n != listenKeep {
		t.Fatalf("follow cleared: kept %d, want %d", n, listenKeep)
	}
	if err := tr.TrimGroupStore(testGroup, transport.GroupFlags{}); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, tr); n != 0 {
		t.Fatalf("both cleared: kept %d", n)
	}
}

// Rows stored under flags that have since changed (the daemon was down, or
// the store predates the change) are trimmed by the sweep.
func TestSweepTrimsToCurrentPolicy(t *testing.T) {
	tr, _ := storeTransport(t, false, false)
	for i := 0; i < 5; i++ {
		tr.storeGroupMessage(testGroup, "s"+time.Duration(i).String(), "x", "1@s.whatsapp.net", false, "t", "messageText",
			time.Now(), "", true)
	}
	if n := countRows(t, tr); n != 5 {
		t.Fatalf("setup stored %d", n)
	}
	if err := tr.sweepGroupStore(); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, tr); n != 0 {
		t.Fatalf("off group kept %d rows after the sweep", n)
	}
}
