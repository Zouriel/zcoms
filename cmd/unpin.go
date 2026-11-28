package cmd

import (
	"fmt"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"

	"github.com/spf13/cobra"
)

func init() {
	var all bool
	unpinCmd := &cobra.Command{
		Use:          "unpin <@username|contact|group|chat id> [message id|last]",
		Short:        "Unpin a message (or all with --all) in a Telegram chat",
		SilenceUsage: true,
		Args:         cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			if all {
				if len(args) > 1 {
					return fmt.Errorf("--all takes no message id")
				}
				return unpinAll(target)
			}
			if len(args) < 2 {
				return fmt.Errorf("give a message id or \"last\", or use --all")
			}
			msgID, err := parseMessageRef(args[1])
			if err != nil {
				return err
			}
			chatID, id, err := runMessageOp(target, msgID,
				func(c *client.Client) (client.Response, error) { return c.Unpin(target, msgID) },
				telegram.UnpinMessage)
			if err != nil {
				return err
			}
			fmt.Printf("Message unpinned ✅ (message_id=%d, chat_id=%d)\n", id, chatID)
			return nil
		},
	}
	unpinCmd.Flags().BoolVar(&all, "all", false, "Unpin every pinned message in the chat")
	tgCmd.AddCommand(unpinCmd)
}

// unpinAll unpins every pinned message in target. It needs no message id, so
// it does not go through runMessageOp's "last" lookup.
func unpinAll(target string) error {
	var chatID int64
	if c, ok := daemonClient(); ok {
		resp, err := c.UnpinAll(target)
		if err != nil {
			return err
		}
		chatID = resp.ChatID
	} else {
		apiID, apiHash, err := resolveTelegramCredentials()
		if err != nil {
			return err
		}
		td, clientID, err := startTDLibClient()
		if err != nil {
			return err
		}
		defer td.Close()
		if err := waitUntilReady(td, clientID, apiID, apiHash); err != nil {
			return err
		}
		if chatID, err = resolveChatTarget(td, clientID, target); err != nil {
			return err
		}
		if err := telegram.UnpinAllMessages(td, clientID, chatID); err != nil {
			return err
		}
	}
	fmt.Printf("All messages unpinned ✅ (chat_id=%d)\n", chatID)
	return nil
}
