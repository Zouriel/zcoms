package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// maxGroupMediaBytes caps one downloaded group attachment. A larger file is
// skipped and the call's text says so.
const maxGroupMediaBytes = 20 << 20

// groupMediaTimeout bounds one group attachment download.
const groupMediaTimeout = 90 * time.Second

// groupKind is messageKind plus stickers. Stickers stay "messageText" in 1:1
// chats (the bridge would otherwise expect a file), but a sticker can be a
// group call (a reply to the account) and its file is fetched then.
func groupKind(m *waE2E.Message) string {
	if m.GetStickerMessage() != nil {
		return "messageSticker"
	}
	return messageKind(m)
}

// groupMedia describes a message's downloadable attachment.
type groupMedia struct {
	label    string // photo, video, document, voice, audio, sticker
	size     uint64 // declared size, 0 when unknown
	mimetype string
	fileName string // a document's own name
}

// mediaOf returns the attachment a group message carries, if any.
func mediaOf(m *waE2E.Message) (groupMedia, bool) {
	switch {
	case m == nil:
		return groupMedia{}, false
	case m.GetImageMessage() != nil:
		x := m.GetImageMessage()
		return groupMedia{label: "photo", size: x.GetFileLength(), mimetype: x.GetMimetype()}, true
	case m.GetVideoMessage() != nil:
		x := m.GetVideoMessage()
		return groupMedia{label: "video", size: x.GetFileLength(), mimetype: x.GetMimetype()}, true
	case m.GetDocumentMessage() != nil:
		x := m.GetDocumentMessage()
		return groupMedia{label: "document", size: x.GetFileLength(), mimetype: x.GetMimetype(), fileName: x.GetFileName()}, true
	case m.GetAudioMessage() != nil:
		x := m.GetAudioMessage()
		label := "audio"
		if x.GetPTT() {
			label = "voice"
		}
		return groupMedia{label: label, size: x.GetFileLength(), mimetype: x.GetMimetype()}, true
	case m.GetStickerMessage() != nil:
		x := m.GetStickerMessage()
		return groupMedia{label: "sticker", size: x.GetFileLength(), mimetype: x.GetMimetype()}, true
	}
	return groupMedia{}, false
}

// downloadBytes fetches an attachment's bytes (a hook in tests).
func (t *Transport) downloadBytes(m *waE2E.Message) ([]byte, error) {
	if t.download != nil {
		return t.download(m)
	}
	t.mu.Lock()
	c := t.client
	t.mu.Unlock()
	if c == nil {
		return nil, errors.New("whatsapp is not connected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), groupMediaTimeout)
	defer cancel()
	return c.DownloadAny(ctx, m)
}

// groupMediaDir is a group's folder under the existing media folder:
// whatsmeow-media/groups/<group id>.
func (t *Transport) groupMediaDir(chat string) string {
	id := chat
	if i := strings.IndexByte(id, '@'); i > 0 {
		id = id[:i]
	}
	return filepath.Join(filepath.Dir(t.dbPath), "whatsmeow-media", "groups", sanitizeName(id))
}

// groupCallMedia downloads the attachment of a message involved in a group
// call (the call itself, or the message it quotes) into the group's media
// folder. It returns the local path, or a note for the call's text when the
// file was skipped or failed. A message without media yields neither. Only
// calls reach here: nothing else from a group is ever downloaded.
func (t *Transport) groupCallMedia(chat, msgID string, m *waE2E.Message, quoted bool) (string, string) {
	gm, ok := mediaOf(m)
	if !ok {
		return "", ""
	}
	what := gm.label
	if quoted {
		what = "quoted " + gm.label
	}
	if gm.size > maxGroupMediaBytes {
		return "", tooLargeNote(what)
	}
	data, err := t.downloadBytes(m)
	if err != nil {
		return "", "[" + what + " could not be downloaded]"
	}
	if len(data) > maxGroupMediaBytes {
		return "", tooLargeNote(what)
	}
	dir := t.groupMediaDir(chat)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "[" + what + " could not be saved]"
	}
	if msgID == "" {
		msgID = "file"
	}
	name := sanitizeName(msgID) + preferredExt(gm.mimetype)
	if fn := strings.TrimSpace(gm.fileName); fn != "" {
		name = sanitizeName(msgID + "-" + fn)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", "[" + what + " could not be saved]"
	}
	return path, ""
}

func tooLargeNote(what string) string {
	return fmt.Sprintf("[%s not downloaded: larger than %d MB]", what, maxGroupMediaBytes>>20)
}

// withNotes appends skipped-media notes to a call's text.
func withNotes(text string, notes ...string) string {
	for _, n := range notes {
		if n == "" {
			continue
		}
		if text != "" {
			text += "\n"
		}
		text += n
	}
	return text
}
