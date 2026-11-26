package hub

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/comms/transport"
)

const (
	testGroupChat = -1001234567890
	testMe        = 777
)

// mediaTransport is a Telegram transport over a listen group whose file
// downloads are faked and counted.
func mediaTransport(t *testing.T, quoted map[int64]telegram.Message) (*telegramTransport, *[]int32) {
	t.Helper()
	d, _ := groupDaemon(t, true, false)
	d.me = testMe
	tr := newTelegramTransport(d)
	tr.mediaRoot = t.TempDir()
	cache := t.TempDir()
	var fetched []int32
	tr.fetchFile = func(f telegram.File) (string, error) {
		fetched = append(fetched, f.ID)
		p := filepath.Join(cache, fmt.Sprintf("f%d", f.ID))
		return p, os.WriteFile(p, []byte("bytes"), 0o600)
	}
	tr.fetchMessage = func(chatID, id int64) (telegram.Message, error) {
		if m, ok := quoted[id]; ok {
			return m, nil
		}
		return telegram.Message{}, fmt.Errorf("no message %d", id)
	}
	return tr, &fetched
}

func photoUpdate(id int64, caption string, mention bool, replyTo int64) string {
	entities := "[]"
	if mention {
		entities = fmt.Sprintf(`[{"offset":0,"length":3,"type":{"@type":"textEntityTypeMentionName","user_id":%d}}]`, testMe)
	}
	reply := ""
	if replyTo != 0 {
		reply = fmt.Sprintf(`,"reply_to":{"@type":"messageReplyToMessage","chat_id":%d,"message_id":%d}`, testGroupChat, replyTo)
	}
	return fmt.Sprintf(`{"@type":"updateNewMessage","message":{"id":%d,"chat_id":%d,"date":1764000000,"is_outgoing":false,`+
		`"sender_id":{"@type":"messageSenderUser","user_id":5550001}%s,`+
		`"content":{"@type":"messagePhoto","photo":{"sizes":[{"type":"x","width":10,"height":10,"photo":{"id":%d,"size":5}}]},`+
		`"caption":{"text":%q,"entities":%s}}}}`, id, testGroupChat, reply, id, caption, entities)
}

func textUpdate(id int64, text string, mention bool, replyTo int64) string {
	entities := "[]"
	if mention {
		entities = fmt.Sprintf(`[{"offset":0,"length":3,"type":{"@type":"textEntityTypeMentionName","user_id":%d}}]`, testMe)
	}
	reply := ""
	if replyTo != 0 {
		reply = fmt.Sprintf(`,"reply_to":{"@type":"messageReplyToMessage","chat_id":%d,"message_id":%d}`, testGroupChat, replyTo)
	}
	return fmt.Sprintf(`{"@type":"updateNewMessage","message":{"id":%d,"chat_id":%d,"date":1764000000,"is_outgoing":false,`+
		`"sender_id":{"@type":"messageSenderUser","user_id":5550001}%s,`+
		`"content":{"@type":"messageText","text":{"text":%q,"entities":%s}}}}`, id, testGroupChat, reply, text, entities)
}

func takeInbound(ch chan transport.Inbound) (transport.Inbound, bool) {
	select {
	case in := <-ch:
		return in, true
	default:
		return transport.Inbound{}, false
	}
}

func TestTelegramNonCallMediaIsNotDownloaded(t *testing.T) {
	tr, fetched := mediaTransport(t, nil)
	ch := make(chan transport.Inbound, 1)
	tr.dispatchUpdate(photoUpdate(100, "look", false, 0), ch)
	if in, ok := takeInbound(ch); ok {
		t.Fatalf("non-call produced %+v", in)
	}
	if len(*fetched) != 0 {
		t.Fatalf("non-call media was downloaded: %v", *fetched)
	}
}

func TestTelegramCallMediaIsDownloaded(t *testing.T) {
	tr, fetched := mediaTransport(t, nil)
	ch := make(chan transport.Inbound, 1)
	tr.dispatchUpdate(photoUpdate(101, "bot what is this", true, 0), ch)
	in, ok := takeInbound(ch)
	if !ok || in.Call != transport.CallMention {
		t.Fatalf("no call: %+v", in)
	}
	if len(*fetched) != 1 || len(in.Files) != 1 {
		t.Fatalf("call media not downloaded: fetched %v files %v", *fetched, in.Files)
	}
	wantDir := filepath.Join(tr.mediaRoot, fmt.Sprint(testGroupChat))
	if filepath.Dir(in.Files[0]) != wantDir {
		t.Fatalf("saved at %s, want under %s", in.Files[0], wantDir)
	}
	if b, err := os.ReadFile(in.Files[0]); err != nil || string(b) != "bytes" {
		t.Fatalf("saved file: %q %v", b, err)
	}
	if in.ReplyFile != "" {
		t.Fatalf("no quote, yet ReplyFile %q", in.ReplyFile)
	}
}

func TestTelegramQuotedMediaIsDownloaded(t *testing.T) {
	quoted := telegram.Message{ID: 90, ChatID: testGroupChat, SenderID: telegram.SenderID{Type: "messageSenderUser", UserID: 5550002}}
	quoted.Content.Type = "messageDocument"
	quoted.Content.Document = &struct {
		FileName string        `json:"file_name"`
		Document telegram.File `json:"document"`
	}{FileName: "plan.pdf", Document: telegram.File{ID: 9, Size: 10}}
	tr, fetched := mediaTransport(t, map[int64]telegram.Message{90: quoted})
	ch := make(chan transport.Inbound, 1)
	tr.dispatchUpdate(textUpdate(102, "bot summarise this", true, 90), ch)
	in, ok := takeInbound(ch)
	if !ok {
		t.Fatal("no call")
	}
	if len(in.Files) != 0 {
		t.Fatalf("a text call has no own file: %v", in.Files)
	}
	if in.ReplyFile == "" || !strings.HasSuffix(in.ReplyFile, "90-plan.pdf") || len(*fetched) != 1 {
		t.Fatalf("quoted media: %q fetched %v", in.ReplyFile, *fetched)
	}

	ev := tr.d.groupEvent(in)
	if ev.ReplyFile != in.ReplyFile {
		t.Fatalf("event lost ReplyFile: %+v", ev)
	}
}

func TestTelegramCallMediaOverCapIsSkipped(t *testing.T) {
	tr, fetched := mediaTransport(t, nil)
	ch := make(chan transport.Inbound, 1)
	up := strings.Replace(photoUpdate(103, "bot", true, 0), `"size":5`, fmt.Sprintf(`"size":%d`, maxGroupMediaBytes+1), 1)
	tr.dispatchUpdate(up, ch)
	in, ok := takeInbound(ch)
	if !ok {
		t.Fatal("no call")
	}
	if len(*fetched) != 0 || len(in.Files) != 0 {
		t.Fatalf("oversized media was downloaded: %v %v", *fetched, in.Files)
	}
	if !strings.Contains(in.Text, "larger than 20 MB") {
		t.Fatalf("text has no note: %q", in.Text)
	}
}

func TestGroupEventCarriesCallFile(t *testing.T) {
	d := newTestDaemon()
	ev := d.groupEvent(transport.Inbound{
		From: transport.Address{Transport: "whatsapp", ID: "1@g.us"}, Group: true,
		Files: []string{"/tmp/x.jpg"}, ReplyFile: "/tmp/y.pdf",
	})
	if ev.File != "/tmp/x.jpg" || ev.ReplyFile != "/tmp/y.pdf" {
		t.Fatalf("event files: %+v", ev)
	}
}

type trimRecorder struct {
	fakeTransport
	got []transport.GroupFlags
}

func (r *trimRecorder) TrimGroupStore(nativeID string, f transport.GroupFlags) error {
	r.got = append(r.got, f)
	return nil
}

func TestGroupSetTrimsStoredMessages(t *testing.T) {
	d, g := groupDaemon(t, true, true)
	rec := &trimRecorder{fakeTransport: fakeTransport{name: "telegram"}}
	d.registry["telegram"] = rec
	off := false
	resp, err := d.groupOp(clientRequest("group_set", g.NativeID, nil, &off))
	if err != nil || resp.Group == nil || resp.Group.Follow {
		t.Fatalf("group_set: %+v %v", resp, err)
	}
	if len(rec.got) != 1 || !rec.got[0].Listen || rec.got[0].Follow {
		t.Fatalf("trim not applied with the new flags: %+v", rec.got)
	}
}

func clientRequest(op, to string, listen, follow *bool) client.Request {
	return client.Request{Op: op, Transport: "telegram", To: to, Listen: listen, Follow: follow}
}
