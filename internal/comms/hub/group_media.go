package hub

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
)

// maxGroupMediaBytes caps one downloaded group attachment. A larger file is
// skipped and the call's text says so.
const maxGroupMediaBytes = 20 << 20

// groupMediaDownloadTimeout bounds one group attachment download.
const groupMediaDownloadTimeout = 90 * time.Second

// telegramGroupMediaRoot is where group call attachments are copied, one
// folder per group chat id: <app dir>/tg-media/groups/<chat id>.
func telegramGroupMediaRoot() string {
	dir, err := client.DefaultAppDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "tg-media", "groups")
}

// fetchMessageFor returns one message of a chat (a hook in tests).
func (t *telegramTransport) fetchMessageFor(chatID, messageID int64) (telegram.Message, error) {
	if t.fetchMessage != nil {
		return t.fetchMessage(chatID, messageID)
	}
	if t.d.tdjson == nil {
		return telegram.Message{}, fmt.Errorf("no telegram session")
	}
	return telegram.FetchMessage(t.d.tdjson, t.d.clientID, chatID, messageID)
}

// fetchFileFor downloads a TDLib file and returns its path in TDLib's cache
// (a hook in tests).
func (t *telegramTransport) fetchFileFor(f telegram.File) (string, error) {
	if t.fetchFile != nil {
		return t.fetchFile(f)
	}
	if f.Local.IsDownloadingCompleted && f.Local.Path != "" {
		return f.Local.Path, nil
	}
	if t.d.tdjson == nil {
		return "", fmt.Errorf("no telegram session")
	}
	return telegram.DownloadFile(t.d.tdjson, t.d.clientID, f.ID, groupMediaDownloadTimeout)
}

// groupCallMedia downloads the attachment of a message involved in a group
// call (the call itself, or the message it quotes) into the group's media
// folder. It returns the local path, or a note for the call's text when the
// file was skipped or failed. A message without media yields neither. Only
// calls reach here: nothing else from a group is ever downloaded.
func (t *telegramTransport) groupCallMedia(chatID int64, m telegram.Message, quoted bool) (string, string) {
	f, name, label, ok := m.Content.MediaFile()
	if !ok || f.ID == 0 {
		return "", ""
	}
	what := label
	if quoted {
		what = "quoted " + label
	}
	size := f.Size
	if size == 0 {
		size = f.ExpectedSize
	}
	if size > maxGroupMediaBytes {
		return "", tooLargeNote(what)
	}
	src, err := t.fetchFileFor(f)
	if err != nil {
		t.d.logf("downloading a %s in group %d: %v", what, chatID, err)
		return "", "[" + what + " could not be downloaded]"
	}
	if st, err := os.Stat(src); err == nil && st.Size() > maxGroupMediaBytes {
		return "", tooLargeNote(what)
	}
	root := t.mediaRoot
	if root == "" {
		return "", "[" + what + " could not be saved]"
	}
	dir := filepath.Join(root, strconv.FormatInt(chatID, 10))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.d.logf("group media folder: %v", err)
		return "", "[" + what + " could not be saved]"
	}
	dst := filepath.Join(dir, safeFileName(strconv.FormatInt(m.ID, 10)+"-"+name))
	if err := copyLocalFile(src, dst); err != nil {
		t.d.logf("saving a %s in group %d: %v", what, chatID, err)
		return "", "[" + what + " could not be saved]"
	}
	return dst, ""
}

func tooLargeNote(what string) string {
	return fmt.Sprintf("[%s not downloaded: larger than %d MB]", what, maxGroupMediaBytes>>20)
}

// safeFileName keeps a file name inside its folder.
func safeFileName(s string) string {
	s = filepath.Base(strings.TrimSpace(s))
	s = strings.NewReplacer("/", "_", "\\", "_", "\n", "_", "\r", "_").Replace(s)
	if s == "" || s == "." || s == ".." {
		return "file"
	}
	return s
}

func copyLocalFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(in, maxGroupMediaBytes+1)); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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
