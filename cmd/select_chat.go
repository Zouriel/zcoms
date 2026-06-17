package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"zcoms/internal/tdlib"

	"github.com/spf13/cobra"
)

func init() {
	var limit int
	var jsonOutput bool

	selectChatCommand := &cobra.Command{
		Use:   "chats",
		Short: "Select a chat and tail it",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireNoDaemon("chats"); err != nil {
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

			if limit <= 0 {
				limit = 20
			}

			chatIDs, err := tdlib.FetchChatIdentifiers(tdjson, clientID, limit)
			if err != nil {
				return err
			}
			if len(chatIDs) == 0 {
				return fmt.Errorf("no chats found")
			}

			if jsonOutput {
				for i, id := range chatIDs {
					title, _ := tdlib.FetchChatTitle(tdjson, clientID, id)
					b, err := json.Marshal(struct {
						Index  int    `json:"index"`
						ChatID int64  `json:"chat_id"`
						Title  string `json:"title"`
					}{Index: i, ChatID: id, Title: title})
					if err == nil {
						fmt.Println(string(b))
					}
				}
				return nil
			}

			fmt.Println("Select a chat:")
			for i, id := range chatIDs {
				title, _ := tdlib.FetchChatTitle(tdjson, clientID, id)
				fmt.Printf("[%d] %s  (chat_id=%d)\n", i, title, id)
			}

			fmt.Print("Enter number: ")
			reader := bufio.NewReader(os.Stdin)
			line, _ := reader.ReadString('\n')
			line = strings.TrimSpace(line)

			idx, err := strconv.Atoi(line)
			if err != nil || idx < 0 || idx >= len(chatIDs) {
				return fmt.Errorf("invalid selection")
			}

			chatID := chatIDs[idx]

			fmt.Println()
			fmt.Println("Tailing selected chat...")
			return executeChatFollow(tdjson, clientID, chatID)
		},
	}

	selectChatCommand.Flags().IntVarP(&limit, "limit", "n", 20, "Number of chats to list")
	selectChatCommand.Flags().BoolVar(&jsonOutput, "json", false, "List chats as JSON lines and exit")
	tgCmd.AddCommand(selectChatCommand)
}
