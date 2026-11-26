package hub

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/comms/transport"
	"github.com/Zouriel/zcoms/internal/comms/whatsapp"
)

func TestWhatsAppTopicOpsSayNoThreads(t *testing.T) {
	d, _ := groupDaemon(t, true, false)
	d.registry["whatsapp"] = whatsapp.New(filepath.Join(t.TempDir(), "wa.db"))
	for _, op := range []string{"topic_create", "topic_list"} {
		_, err := d.groupOp(client.Request{Op: op, Transport: "whatsapp", To: "120363000000000001@g.us", Name: "bug"})
		if err == nil || err.Error() != "WhatsApp groups do not have threads" {
			t.Fatalf("%s: got %v", op, err)
		}
	}
	if th, ignored := d.sendThread("whatsapp", "login bug"); th != "" || !ignored {
		t.Fatalf("whatsapp thread = %q ignored=%v, want dropped", th, ignored)
	}
	d.registry["telegram"] = newTelegramTransport(d)
	if th, ignored := d.sendThread("telegram", "12"); th != "12" || ignored {
		t.Fatalf("telegram thread = %q ignored=%v", th, ignored)
	}
	if th, ignored := d.sendThread("whatsapp", ""); th != "" || ignored {
		t.Fatal("no thread asked must not be flagged")
	}
}

func TestAgentTopicOpsNeedAGroupThatIsOn(t *testing.T) {
	d, g := groupDaemon(t, false, false)
	d.registry["telegram"] = newTelegramTransport(d)
	_, err := d.groupOp(client.Request{Op: "topic_create", Transport: "telegram", To: g.NativeID, Name: "x", Caller: "agent"})
	if err == nil || !strings.Contains(err.Error(), "only in a group that is on") {
		t.Fatalf("got %v", err)
	}
}

func TestThreadIDPropagatesFromCallToEvent(t *testing.T) {
	d, _ := groupDaemon(t, true, false)
	update := `{"@type":"updateNewMessage","message":{"id":7340033,"chat_id":-1001234567890,"date":1764000000,"is_outgoing":false,` +
		`"sender_id":{"@type":"messageSenderUser","user_id":5550001},"is_topic_message":true,` +
		`"topic_id":{"@type":"messageTopicForum","forum_topic_id":12},` +
		`"content":{"@type":"messageText","text":{"text":"@zagent hi","entities":[{"offset":0,"length":7,"type":{"@type":"textEntityTypeMention"}}]}}}}`
	in, ok := dispatchOne(t, d, update, func(int64, int64) bool { return false })
	if !ok || in.ThreadID != "12" {
		t.Fatalf("inbound thread: %+v %v", in, ok)
	}
	ch := make(chan client.Event, 1)
	d.addSubscriber("agent", ch)
	in.At = time.Now()
	d.deliverInbound(in)
	if ev := <-ch; ev.ThreadID != "12" {
		t.Fatalf("event thread = %q", ev.ThreadID)
	}
	plain, ok := dispatchOne(t, d, mentionUpdate, func(int64, int64) bool { return false })
	if !ok || plain.ThreadID != "" {
		t.Fatalf("call outside a thread: %+v", plain)
	}
}

func TestPickTopic(t *testing.T) {
	fts := []telegram.ForumTopic{{ID: 1, Name: "General"}, {ID: 12, Name: "Login bug"}, {ID: 13, Name: "Login page"}}
	if id, err := pickTopic(fts, "login BUG"); err != nil || id != 12 {
		t.Fatalf("exact: %d %v", id, err)
	}
	if _, err := pickTopic(fts, "login"); err == nil || !strings.Contains(err.Error(), "say which one") {
		t.Fatalf("ambiguous: %v", err)
	}
	if _, err := pickTopic(fts, "deploy"); err == nil {
		t.Fatal("unknown name must fail")
	}
	d, _ := groupDaemon(t, true, false)
	if id, err := d.threadID(-100, " 12 "); err != nil || id != 12 {
		t.Fatalf("numeric thread: %d %v", id, err)
	}
	if id, err := d.threadID(-100, ""); err != nil || id != 0 {
		t.Fatalf("empty thread: %d %v", id, err)
	}
}

func TestCheckSendFileCap(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "a.png")
	if err := os.WriteFile(small, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := checkSendFile(small); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxSendFileBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := checkSendFile(big); err == nil || !strings.Contains(err.Error(), "20 MB") {
		t.Fatalf("big file: %v", err)
	}
	if err := checkSendFile(dir); err == nil {
		t.Fatal("a directory must be refused")
	}
}

var _ transport.Topicer = (*telegramTransport)(nil)
