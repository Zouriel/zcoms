package hub

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
)

// maxReadDownloads caps how many media files one read fetches, so a snapshot of
// a media-heavy chat can't trigger an unbounded run of blocking downloads.
const maxReadDownloads = 8

// maxIPCReadCount caps how many messages one read op pulls.
const maxIPCReadCount = 200

// downloadMessageMedia downloads a message's attachment (if any) and returns its
// local path within TDLib's cache, or "" when there's nothing to fetch.
func (d *daemon) downloadMessageMedia(m telegram.Message) string {
	f, _, _, ok := m.Content.MediaFile()
	if !ok || f.ID == 0 {
		return ""
	}
	if f.Local.IsDownloadingCompleted && f.Local.Path != "" {
		return f.Local.Path
	}
	path, err := telegram.DownloadFile(d.tdjson, d.clientID, f.ID, 90*time.Second)
	if err != nil {
		return ""
	}
	return path
}

// buildMessage renders a history message into the wire shape the CLI prints,
// resolving the sender's display name and the media kind/label. When download is
// set, an attachment is fetched and its local path included.
func (d *daemon) buildMessage(m telegram.Message, titleCache map[int64]string, download bool) client.Message {
	sender := "unknown"
	switch m.SenderID.Type {
	case "messageSenderUser":
		sender = d.senderName(m.SenderID.UserID)
	case "messageSenderChat":
		cid := m.SenderID.ChatID
		if cached, ok := titleCache[cid]; ok && cached != "" {
			sender = cached
		} else if title, err := telegram.FetchChatTitle(d.tdjson, d.clientID, cid); err == nil && title != "" {
			titleCache[cid] = title
			sender = title
		} else {
			sender = fmt.Sprintf("chat:%d", cid)
		}
	}

	kind := "text"
	if m.Content.Type != "messageText" {
		if _, _, label, isMedia := m.Content.MediaFile(); isMedia {
			kind = label
		} else {
			kind = strings.TrimPrefix(m.Content.Type, "message")
		}
	}

	file := ""
	if download && kind != "text" {
		file = d.downloadMessageMedia(m)
	}

	return client.Message{
		MessageID: m.ID,
		ChatID:    m.ChatID,
		Date:      m.Date,
		Outgoing:  m.IsOutgoing,
		Sender:    sender,
		Kind:      kind,
		Text:      m.Content.CaptionOrText(),
		File:      file,
	}
}

// collectUnreadTG scans Telegram for unread 1:1 incoming messages from non-owner
// senders, the data source for the daemon's "unread" IPC op. Comms returns the
// raw inbox (no allow-list / claims filtering — the agent tier applies its own
// policy and merges WhatsApp via the sidecar).
func (d *daemon) collectUnreadTG() []client.UnreadItem {
	chatIDs, err := telegram.FetchChatIdentifiers(d.tdjson, d.clientID, 80)
	if err != nil {
		fmt.Printf("[comms] couldn't list chats: %v\n", err)
		return nil
	}

	var items []client.UnreadItem
	seen := map[int64]bool{}
	for _, cid := range chatIDs {
		if seen[cid] {
			continue
		}
		seen[cid] = true

		info, err := telegram.FetchChatInfo(d.tdjson, d.clientID, cid)
		if err != nil || info.UnreadCount == 0 || info.TypeName != "private" {
			continue
		}
		unread, err := telegram.FetchUnreadIncoming(d.tdjson, d.clientID, cid, info.LastReadInboxMessageID)
		if err != nil || len(unread) == 0 {
			continue
		}
		name := d.senderName(info.UserID)
		for _, m := range unread {
			items = append(items, client.UnreadItem{
				Sender: name,
				Text:   replyText(m.Content),
				When:   m.Date,
				ChatID: cid,
				MsgID:  m.ID,
			})
		}
	}
	return items
}
