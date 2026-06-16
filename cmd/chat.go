package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tg/internal/agent"
	"tg/internal/tdlib"

	"github.com/spf13/cobra"
)

// chatMessage is the machine-readable shape emitted by `tg chat --json`.
type chatMessage struct {
	MessageID int64  `json:"message_id"`
	ChatID    int64  `json:"chat_id"`
	Date      int64  `json:"date"`
	Outgoing  bool   `json:"outgoing"`
	Sender    string `json:"sender"`
	Kind      string `json:"kind"`           // "text" or a media label
	Text      string `json:"text"`           // text or caption
	FilePath  string `json:"file,omitempty"` // local path if media was downloaded
}

func init() {
	var (
		waitForReply bool
		timeout      time.Duration
		readCount    int
		jsonOutput   bool
		download     bool
	)

	chatCommand := &cobra.Command{
		Use:   "chat <@username|chat_id> [message]",
		Short: "One round-trip chat: send and/or wait for the next reply (scriptable)",
		Long: "Send a message and wait for the reply, or wait for the next incoming\n" +
			"message, or snapshot recent history — each as a single command that exits\n" +
			"when done. Designed to be driven from scripts and agents rather than typed\n" +
			"at interactively (see `tail` for the interactive REPL).",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(args[0])
			message := strings.Join(args[1:], " ")

			// Route through the daemon when it's running (it owns the session).
			if agent.DaemonRunning() {
				if readCount > 0 {
					handled, msgs, err := agent.DaemonRead(target, readCount, download)
					if handled {
						if err != nil {
							return err
						}
						for _, m := range msgs {
							emitChatMessage(chatMessage{
								MessageID: m.MessageID,
								ChatID:    m.ChatID,
								Date:      m.Date,
								Outgoing:  m.Outgoing,
								Sender:    m.Sender,
								Kind:      m.Kind,
								Text:      m.Text,
								FilePath:  m.File,
							}, jsonOutput)
						}
						return nil
					}
				}
				if message != "" && !waitForReply {
					handled, _, _, err := agent.DaemonSend(target, message)
					if handled {
						return err
					}
				} else {
					handled, reply, err := agent.DaemonChatWait(target, message, timeout)
					if handled {
						if err != nil {
							return err
						}
						emitChatMessage(chatMessage{Sender: target, Kind: "text", Text: reply}, jsonOutput)
						return nil
					}
				}
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

			nameCache := map[int64]string{}
			titleCache := map[int64]string{}

			// Snapshot mode: print the last N messages and exit, without opening
			// the chat (no read receipts) — matches the daemon-routed read path.
			if readCount > 0 {
				history, err := tdlib.FetchChatHistorySnapshot(tdjson, clientID, chatID, readCount)
				if err != nil {
					return err
				}
				for i := len(history) - 1; i >= 0; i-- {
					m := history[i]
					sender := resolveSenderDisplayName(tdjson, clientID, m.SenderID, nameCache, titleCache)
					emitChatMessage(buildChatMessage(m, sender, ""), jsonOutput)
				}
				return nil
			}

			selfUser, err := tdlib.FetchCurrentUser(tdjson, clientID)
			if err != nil {
				return err
			}

			startTime := time.Now().Unix()

			if message != "" {
				if _, err := tdlib.SendTextMessage(tdjson, clientID, chatID, message); err != nil {
					return err
				}
				if !waitForReply {
					return nil
				}
			}

			var deadline time.Time
			if timeout > 0 {
				deadline = time.Now().Add(timeout)
			}

			for {
				if !deadline.IsZero() && time.Now().After(deadline) {
					return fmt.Errorf("timed out after %s waiting for a reply", timeout)
				}

				updateJSON, err := tdlib.ReceiveUpdates(tdjson)
				if err != nil || updateJSON == "" {
					continue
				}

				u, ok := tdlib.ParseUpdateNewMessage(updateJSON)
				if !ok || u.Message.ChatID != chatID {
					continue
				}
				// Only genuinely new, inbound messages from the other side.
				if u.Message.IsOutgoing || u.Message.Date < startTime {
					continue
				}
				if u.Message.SenderID.Type == "messageSenderUser" && u.Message.SenderID.UserID == selfUser.ID {
					continue
				}

				// Media is not downloaded by default; pass --download to fetch it
				// (use only for trusted senders — see `tg download` for a picker).
				filePath := ""
				if download {
					if path, _, isMedia, dlErr := downloadIncomingMedia(tdjson, clientID, chatTitle, chatID, u.Message.Content); isMedia && dlErr == nil {
						filePath = path
					}
				}

				sender := resolveSenderDisplayName(tdjson, clientID, u.Message.SenderID, nameCache, titleCache)
				emitChatMessage(buildChatMessage(u.Message, sender, filePath), jsonOutput)
				return nil
			}
		},
	}

	chatCommand.Flags().BoolVarP(&waitForReply, "wait", "w", true, "Wait for the next reply after sending")
	chatCommand.Flags().DurationVarP(&timeout, "timeout", "t", 0, "Max time to wait for a reply (0 = no limit)")
	chatCommand.Flags().IntVarP(&readCount, "read", "r", 0, "Snapshot mode: print the last N messages and exit")
	chatCommand.Flags().BoolVar(&jsonOutput, "json", false, "Emit messages as JSON lines")
	chatCommand.Flags().BoolVar(&download, "download", false, "Download media in the reply (off by default)")

	rootCmd.AddCommand(chatCommand)
}

func buildChatMessage(m tdlib.Message, sender, filePath string) chatMessage {
	kind := "text"
	if m.Content.Type != "messageText" {
		if _, _, label, isMedia := m.Content.MediaFile(); isMedia {
			kind = label
		} else {
			kind = strings.TrimPrefix(m.Content.Type, "message")
		}
	}
	return chatMessage{
		MessageID: m.ID,
		ChatID:    m.ChatID,
		Date:      m.Date,
		Outgoing:  m.IsOutgoing,
		Sender:    sender,
		Kind:      kind,
		Text:      m.Content.CaptionOrText(),
		FilePath:  filePath,
	}
}

func emitChatMessage(msg chatMessage, jsonOutput bool) {
	if jsonOutput {
		encoded, err := json.Marshal(msg)
		if err == nil {
			fmt.Println(string(encoded))
			return
		}
	}

	body := msg.Text
	if msg.Kind != "text" {
		if body != "" {
			body = fmt.Sprintf("[%s] %s", msg.Kind, body)
		} else {
			body = fmt.Sprintf("[%s]", msg.Kind)
		}
	}
	fmt.Println(body)
	if msg.FilePath != "" {
		fmt.Printf("[saved: %s]\n", msg.FilePath)
	}
}
