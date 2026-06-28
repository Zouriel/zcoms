package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Zouriel/zcoms/internal/tdlib"

	"github.com/spf13/cobra"
)

// mediaItem is one downloadable media message in a chat's recent history.
type mediaItem struct {
	Index     int    `json:"index"`
	MessageID int64  `json:"message_id"`
	Date      int64  `json:"date"`
	Sender    string `json:"sender"`
	Kind      string `json:"kind"`
	FileName  string `json:"file_name"`
	Size      int64  `json:"size"`
	Caption   string `json:"caption"`

	content tdlib.Content
}

func init() {
	var (
		limit      int
		jsonOutput bool
		pick       int
	)

	downloadCommand := &cobra.Command{
		Use:   "download <@username|chat_id>",
		Short: "List recent received media (newest first) and download a chosen one",
		Long: "Media is never downloaded automatically. This command lists the most\n" +
			"recent media in a chat (newest first); pick one to download it into\n" +
			"~/Downloads/zcoms/<chat>/.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(args[0])

			if err := requireNoDaemon("download"); err != nil {
				return err
			}

			apiID, apiHash, err := resolveTelegramCredentials()
			if err != nil {
				return err
			}

			tdjson, clientID, err := startTDLibClient()
			if err != nil {
				return err
			}
			defer tdjson.Close()

			if err := waitUntilReady(tdjson, clientID, apiID, apiHash); err != nil {
				return err
			}

			chatID, err := resolveChatTarget(tdjson, clientID, target)
			if err != nil {
				return err
			}
			chatTitle, _ := tdlib.FetchChatTitle(tdjson, clientID, chatID)

			items, err := collectRecentMedia(tdjson, clientID, chatID, limit)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				fmt.Println("No media found in the recent messages of this chat.")
				return nil
			}

			if jsonOutput {
				encoded, err := json.MarshalIndent(items, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(encoded))
				return nil
			}

			// Non-interactive selection.
			if pick >= 0 {
				if pick >= len(items) {
					return fmt.Errorf("index %d out of range (have %d items)", pick, len(items))
				}
				return downloadMediaItem(tdjson, clientID, chatTitle, chatID, items[pick])
			}

			// Interactive picker.
			fmt.Printf("Recent media in %q (newest first):\n", chatTitle)
			for _, it := range items {
				fmt.Printf("  [%d] %-9s %s%s  (%s)\n",
					it.Index, it.Kind, it.FileName, captionSuffix(it.Caption), humanSize(it.Size))
			}

			reader := bufio.NewReader(os.Stdin)
			fmt.Print("\nEnter number to download (Enter = newest [0], q to cancel): ")
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)

			if strings.EqualFold(line, "q") {
				fmt.Println("Cancelled.")
				return nil
			}

			selected := 0
			if line != "" {
				selected, err = strconv.Atoi(line)
				if err != nil || selected < 0 || selected >= len(items) {
					return fmt.Errorf("invalid selection")
				}
			}

			return downloadMediaItem(tdjson, clientID, chatTitle, chatID, items[selected])
		},
	}

	downloadCommand.Flags().IntVarP(&limit, "limit", "n", 30, "How many recent messages to scan for media")
	downloadCommand.Flags().BoolVar(&jsonOutput, "json", false, "List media as JSON and exit (no download)")
	downloadCommand.Flags().IntVarP(&pick, "pick", "p", -1, "Download this index non-interactively (0 = newest)")

	tgCmd.AddCommand(downloadCommand)
}

func collectRecentMedia(tdjson *tdlib.TDJSON, clientID int32, chatID int64, limit int) ([]mediaItem, error) {
	history, err := tdlib.FetchChatHistory(tdjson, clientID, chatID, limit)
	if err != nil {
		return nil, err
	}

	nameCache := map[int64]string{}
	titleCache := map[int64]string{}

	var items []mediaItem
	for _, m := range history { // history is newest-first
		file, fileName, label, isMedia := m.Content.MediaFile()
		if !isMedia {
			continue
		}
		items = append(items, mediaItem{
			Index:     len(items),
			MessageID: m.ID,
			Date:      m.Date,
			Sender:    resolveSenderDisplayName(tdjson, clientID, m.SenderID, nameCache, titleCache),
			Kind:      label,
			FileName:  fileName,
			Size:      file.Size,
			Caption:   m.Content.CaptionOrText(),
			content:   m.Content,
		})
	}
	return items, nil
}

func downloadMediaItem(tdjson *tdlib.TDJSON, clientID int32, chatTitle string, chatID int64, item mediaItem) error {
	fmt.Printf("Downloading %s (%s)...\n", item.Kind, item.FileName)
	savedPath, label, ok, err := downloadIncomingMedia(tdjson, clientID, chatTitle, chatID, item.content)
	if !ok {
		return fmt.Errorf("selected message has no downloadable media")
	}
	if err != nil {
		return err
	}
	fmt.Printf("Saved %s → %s\n", label, savedPath)
	return nil
}

func captionSuffix(caption string) string {
	caption = strings.TrimSpace(strings.ReplaceAll(caption, "\n", " "))
	if caption == "" {
		return ""
	}
	if len(caption) > 40 {
		caption = caption[:40] + "…"
	}
	return "  — " + caption
}

func humanSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}
