package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File mirrors the TDLib "file" object (only the fields we use).
type File struct {
	ID    int32 `json:"id"`
	Size  int64 `json:"size"`
	Local struct {
		Path                   string `json:"path"`
		IsDownloadingCompleted bool   `json:"is_downloading_completed"`
	} `json:"local"`
	Remote struct {
		UniqueID string `json:"unique_id"`
	} `json:"remote"`
}

// PhotoSize mirrors the TDLib "photoSize" object.
type PhotoSize struct {
	Type   string `json:"type"`
	Photo  File   `json:"photo"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// MediaFile returns the best downloadable file for a media message, a suggested
// filename, and a short human-readable label ("photo", "video", ...). ok is
// false when the content carries no downloadable file (e.g. plain text).
func (c Content) MediaFile() (file File, fileName string, label string, ok bool) {
	switch c.Type {
	case "messagePhoto":
		if c.Photo == nil || len(c.Photo.Sizes) == 0 {
			return File{}, "", "photo", false
		}
		best := c.Photo.Sizes[0]
		for _, s := range c.Photo.Sizes {
			if s.Photo.Size > best.Photo.Size || s.Width*s.Height > best.Width*best.Height {
				best = s
			}
		}
		return best.Photo, mediaName("", best.Photo, "photo", ".jpg"), "photo", true

	case "messageVideo":
		if c.Video == nil {
			return File{}, "", "video", false
		}
		return c.Video.Video, mediaName(c.Video.FileName, c.Video.Video, "video", ".mp4"), "video", true

	case "messageAudio":
		if c.Audio == nil {
			return File{}, "", "audio", false
		}
		return c.Audio.Audio, mediaName(c.Audio.FileName, c.Audio.Audio, "audio", ".mp3"), "audio", true

	case "messageDocument":
		if c.Document == nil {
			return File{}, "", "document", false
		}
		return c.Document.Document, mediaName(c.Document.FileName, c.Document.Document, "file", ""), "document", true

	case "messageAnimation":
		if c.Animation == nil {
			return File{}, "", "animation", false
		}
		return c.Animation.Animation, mediaName(c.Animation.FileName, c.Animation.Animation, "animation", ".mp4"), "animation", true

	case "messageVoiceNote":
		if c.VoiceNote == nil {
			return File{}, "", "voice", false
		}
		return c.VoiceNote.Voice, mediaName("", c.VoiceNote.Voice, "voice", ".ogg"), "voice", true

	case "messageVideoNote":
		if c.VideoNote == nil {
			return File{}, "", "video note", false
		}
		return c.VideoNote.Video, mediaName("", c.VideoNote.Video, "videonote", ".mp4"), "video note", true

	case "messageSticker":
		if c.Sticker == nil {
			return File{}, "", "sticker", false
		}
		return c.Sticker.Sticker, mediaName("", c.Sticker.Sticker, "sticker", ".webp"), "sticker", true
	}

	return File{}, "", "", false
}

// CaptionOrText returns the caption (for media) or text (for text messages).
func (c Content) CaptionOrText() string {
	if c.Caption.Text != "" {
		return c.Caption.Text
	}
	return c.Text.Text
}

func mediaName(fileName string, f File, prefix, defaultExt string) string {
	if name := strings.TrimSpace(fileName); name != "" {
		return filepath.Base(name)
	}
	unique := f.Remote.UniqueID
	if unique == "" {
		unique = fmt.Sprintf("%d", f.ID)
	}
	return prefix + "_" + unique + defaultExt
}

// DownloadFile downloads a file by id and blocks until it completes, returning
// the local path within TDLib's file cache. Uses a synchronous downloadFile so
// the reply only arrives once the download has finished.
func DownloadFile(tdjson *TDJSON, clientID int32, fileID int32, timeout time.Duration) (string, error) {
	request := map[string]any{
		"@type":       "downloadFile",
		"file_id":     fileID,
		"priority":    1,
		"offset":      0,
		"limit":       0,
		"synchronous": true,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return "", err
	}

	resp, err := SendRequestAndWait(tdjson, clientID, string(requestBytes), "download-file", timeout)
	if err != nil {
		return "", err
	}

	var file File
	if err := json.Unmarshal([]byte(resp), &file); err != nil {
		return "", err
	}
	if !file.Local.IsDownloadingCompleted || file.Local.Path == "" {
		return "", fmt.Errorf("download did not complete")
	}
	return file.Local.Path, nil
}

// classifyOutgoing picks the TDLib inputMessage type and file field for a local
// file based on its extension, so images go as photos, clips as videos, etc.
func classifyOutgoing(path string) (messageType, fileField, label string) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg", ".png", ".webp", ".bmp", ".heic", ".tiff":
		return "inputMessagePhoto", "photo", "photo"
	case ".gif":
		return "inputMessageAnimation", "animation", "animation"
	case ".mp4", ".mov", ".mkv", ".webm", ".avi", ".m4v":
		return "inputMessageVideo", "video", "video"
	case ".mp3", ".m4a", ".flac", ".ogg", ".opus", ".wav", ".aac":
		return "inputMessageAudio", "audio", "audio"
	default:
		return "inputMessageDocument", "document", "document"
	}
}

// SendLocalFileMessage sends a local file to a chat as the appropriate media
// type with an optional caption. It returns the temporary message id (the
// upload runs asynchronously — use WaitForSendCompletion to block until done)
// and a human-readable label for the media kind.
func SendLocalFileMessage(tdjson *TDJSON, clientID int32, chatID int64, path, caption string) (int64, string, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return 0, "", err
	}
	info, err := os.Stat(absolutePath)
	if err != nil {
		return 0, "", fmt.Errorf("file not found: %s", path)
	}
	if info.IsDir() {
		return 0, "", fmt.Errorf("%s is a directory, not a file", path)
	}

	messageType, fileField, label := classifyOutgoing(absolutePath)

	content := map[string]any{
		"@type": messageType,
		fileField: map[string]any{
			"@type": "inputFileLocal",
			"path":  absolutePath,
		},
	}
	if caption != "" {
		content["caption"] = FormatOutgoingText(tdjson, clientID, caption)
	}

	request := map[string]any{
		"@type":                 "sendMessage",
		"chat_id":               chatID,
		"input_message_content": content,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return 0, "", err
	}

	resp, err := SendRequestAndWait(tdjson, clientID, string(requestBytes), "send-file", 30*time.Second)
	if err != nil {
		return 0, "", err
	}

	var out struct {
		Type string `json:"@type"`
		ID   int64  `json:"id"`
	}
	if err := json.Unmarshal([]byte(resp), &out); err != nil {
		return 0, "", fmt.Errorf("failed to parse sendMessage response: %w; resp=%s", err, resp)
	}
	if out.Type != "message" || out.ID == 0 {
		return 0, "", fmt.Errorf("sendMessage unexpected response: %s", resp)
	}

	return out.ID, label, nil
}

// WaitForSendCompletion blocks until the message with the given temporary id
// finishes uploading/sending (or fails). Needed for one-shot commands so the
// process doesn't exit and cancel the upload before it completes.
func WaitForSendCompletion(tdjson *TDJSON, clientID int32, temporaryMessageID int64, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		update, err := ReceiveUpdates(tdjson)
		if err != nil || update == "" {
			continue
		}

		var env struct {
			Type         string `json:"@type"`
			OldMessageID int64  `json:"old_message_id"`
			Error        struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(update), &env) != nil {
			continue
		}
		if env.OldMessageID != temporaryMessageID {
			continue
		}

		switch env.Type {
		case "updateMessageSendSucceeded":
			return nil
		case "updateMessageSendFailed":
			if env.Error.Message != "" {
				return fmt.Errorf("send failed: %s", env.Error.Message)
			}
			return fmt.Errorf("send failed")
		}
	}

	return fmt.Errorf("timed out waiting for upload to finish")
}
