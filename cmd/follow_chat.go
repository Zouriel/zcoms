package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zcoms/internal/tdlib"

	"github.com/spf13/cobra"
)

func init() {
	followChatCommand := &cobra.Command{
		Use:   "tail <chat_id|username>",
		Short: "Tail a Telegram chat (type to send)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(args[0])

			if err := requireNoDaemon("tail"); err != nil {
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

			for {
				state, err := tdlib.FetchAuthorizationState(tdjson, clientID)
				if err != nil {
					return err
				}
				if state == tdlib.AuthStateReady {
					break
				}
				if state == tdlib.AuthStateWaitTdlibParameters {
					if err := tdlib.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
						return err
					}
				}
				time.Sleep(200 * time.Millisecond)
			}

			var chatID int64
			if id, parseErr := strconv.ParseInt(target, 10, 64); parseErr == nil {
				chatID = id
			} else {
				username := strings.TrimPrefix(target, "@")
				chatID, err = tdlib.ResolveChatIdentifierByUsername(tdjson, clientID, username)
				if err != nil {
					return err
				}
			}

			return executeChatFollow(tdjson, clientID, chatID)
		},
	}

	tgCmd.AddCommand(followChatCommand)
}

func executeChatFollow(tdjson *tdlib.TDJSON, clientID int32, chatID int64) error {
	userNameCache := map[int64]string{}
	chatTitleCache := map[int64]string{}

	seen := map[int64]bool{}

	if history, err := tdlib.FetchChatHistory(tdjson, clientID, chatID, 20); err == nil && len(history) > 0 {
		fmt.Println("---- last 20 ----")

		for i := len(history) - 1; i >= 0; i-- {
			m := history[i]

			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true

			sender := resolveSenderDisplayName(tdjson, clientID, m.SenderID, userNameCache, chatTitleCache)
			fmt.Printf("%s: %s\n", sender, formatMessageContent(m.Content))
		}

		fmt.Println("---------------")
	}

	fmt.Println("Tailing chat. Type to send; paste a file path to send a file. Ctrl+C to stop.")

	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}

			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			if path, isFile := looksLikeExistingFile(line); isFile {
				temporaryMessageID, label, sendErr := tdlib.SendLocalFileMessage(tdjson, clientID, chatID, path, "")
				if sendErr != nil {
					fmt.Println("Send failed:", sendErr)
					continue
				}
				fmt.Printf("Uploading %s (%s)...\n", label, filepath.Base(path))
				go func() {
					if err := tdlib.WaitForSendCompletion(tdjson, clientID, temporaryMessageID, mediaDownloadTimeout); err != nil {
						fmt.Println("Upload failed:", err)
					}
				}()
				continue
			}

			if _, err := tdlib.SendTextMessage(tdjson, clientID, chatID, line); err != nil {
				fmt.Println("Send failed:", err)
			}
		}
	}()

	for {
		updateJSON, err := tdlib.ReceiveUpdates(tdjson)
		if err != nil || updateJSON == "" {
			continue
		}

		u, ok := tdlib.ParseUpdateNewMessage(updateJSON)
		if !ok || u.Message.ChatID != chatID {
			continue
		}

		if seen[u.Message.ID] {
			continue
		}
		seen[u.Message.ID] = true

		sender := resolveSenderDisplayName(tdjson, clientID, u.Message.SenderID, userNameCache, chatTitleCache)
		fmt.Printf("%s: %s\n", sender, formatMessageContent(u.Message.Content))

		// Media is never auto-downloaded — use `zc tg download <chat>` to pick and
		// fetch a specific file deliberately.
	}
}

// formatMessageContent renders a message for display: text inline, media as a
// [label] tag with any caption appended.
func formatMessageContent(content tdlib.Content) string {
	if content.Type == "messageText" {
		return content.Text.Text
	}

	_, _, label, isMedia := content.MediaFile()
	if !isMedia {
		return fmt.Sprintf("[%s]", content.Type)
	}

	if caption := content.CaptionOrText(); caption != "" {
		return fmt.Sprintf("[%s] %s", label, caption)
	}
	return fmt.Sprintf("[%s]", label)
}

func resolveSenderDisplayName(
	tdjson *tdlib.TDJSON,
	clientID int32,
	sender tdlib.SenderID,
	userNameCache map[int64]string,
	chatTitleCache map[int64]string,
) string {
	switch sender.Type {
	case "messageSenderUser":
		uid := sender.UserID
		if cached, ok := userNameCache[uid]; ok && cached != "" {
			return cached
		}
		name, err := tdlib.FetchUserDisplayName(tdjson, clientID, uid)
		if err == nil && name != "" {
			userNameCache[uid] = name
			return name
		}
		return fmt.Sprintf("user:%d", uid)

	case "messageSenderChat":
		cid := sender.ChatID
		if cached, ok := chatTitleCache[cid]; ok && cached != "" {
			return cached
		}
		title, err := tdlib.FetchChatTitle(tdjson, clientID, cid)
		if err == nil && title != "" {
			chatTitleCache[cid] = title
			return title
		}
		return fmt.Sprintf("chat:%d", cid)

	default:
		return "unknown"
	}
}
