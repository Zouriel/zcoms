package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/telegram"
)

const mediaDownloadTimeout = 30 * time.Minute

// downloadsRoot is ~/Downloads/zcoms, the base folder under which each
// chat gets its own subfolder.
func downloadsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Downloads", "zcoms"), nil
}

// chatDownloadDir returns (and creates) ~/Downloads/zcoms/<chat>/ for the
// given chat, using a sanitized chat title with a chat-id fallback.
func chatDownloadDir(chatTitle string, chatID int64) (string, error) {
	root, err := downloadsRoot()
	if err != nil {
		return "", err
	}

	folder := sanitizeFolderName(chatTitle)
	if folder == "" {
		folder = fmt.Sprintf("chat_%d", chatID)
	}

	dir := filepath.Join(root, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func sanitizeFolderName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
		"\n", " ", "\r", " ", "\t", " ",
	)
	name = replacer.Replace(name)
	name = strings.Trim(name, " .")
	if len(name) > 100 {
		name = name[:100]
	}
	return name
}

// uniqueDestination returns a path inside dir for fileName that does not clobber
// an existing file (appends _1, _2, ... before the extension if needed).
func uniqueDestination(dir, fileName string) string {
	fileName = sanitizeFolderName(fileName)
	if fileName == "" {
		fileName = "file"
	}
	candidate := filepath.Join(dir, fileName)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}

	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	for i := 1; ; i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}

func copyFile(sourcePath, destinationPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(destinationPath)
	if err != nil {
		return err
	}
	defer destination.Close()

	if _, err := io.Copy(destination, source); err != nil {
		return err
	}
	return destination.Sync()
}

// downloadIncomingMedia downloads the media in msg (if any) and copies it into
// the chat's download folder, returning the saved path. It returns ok=false for
// non-media messages. TDLib keeps the original in its own cache; we copy out so
// the file survives cache cleanup and lives in a predictable place.
func downloadIncomingMedia(
	tdjson *telegram.TDJSON,
	clientID int32,
	chatTitle string,
	chatID int64,
	content telegram.Content,
) (savedPath string, label string, ok bool, err error) {
	file, fileName, label, isMedia := content.MediaFile()
	if !isMedia {
		return "", label, false, nil
	}

	localPath, err := telegram.DownloadFile(tdjson, clientID, file.ID, mediaDownloadTimeout)
	if err != nil {
		return "", label, true, err
	}

	dir, err := chatDownloadDir(chatTitle, chatID)
	if err != nil {
		return "", label, true, err
	}

	if fileName == "" {
		fileName = filepath.Base(localPath)
	}
	destination := uniqueDestination(dir, fileName)

	if err := copyFile(localPath, destination); err != nil {
		return "", label, true, err
	}
	return destination, label, true, nil
}

// expandUserPath expands a leading ~ to the user's home directory.
func expandUserPath(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// looksLikeExistingFile reports whether s points to a readable file (used by
// tail to decide whether a typed line is a file path to send as media).
func looksLikeExistingFile(s string) (string, bool) {
	candidate := expandUserPath(strings.TrimSpace(s))
	if candidate == "" {
		return "", false
	}
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	return candidate, true
}
