package cmd

import (
	"fmt"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"

	"github.com/spf13/cobra"
)

func init() {
	var silent, forSelf bool
	pinCmd := &cobra.Command{
		Use:   "pin <@username|contact|group|chat id> <message id|last>",
		Short: "Pin a message in a Telegram chat",
		Long: "Pin a message by id, or \"last\" for the newest message you sent in\n" +
			"that chat. In a group you need the right to pin messages.",
		SilenceUsage: true,
		Args:         cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			msgID, err := parseMessageRef(args[1])
			if err != nil {
				return err
			}
			o := client.PinOptions{Silent: silent, ForSelf: forSelf}
			chatID, id, err := runMessageOp(target, msgID,
				func(c *client.Client) (client.Response, error) { return c.Pin(target, msgID, o) },
				func(td *telegram.TDJSON, cid int32, chatID, id int64) error {
					return telegram.PinMessage(td, cid, chatID, id, silent, forSelf)
				})
			if err != nil {
				return err
			}
			fmt.Printf("Message pinned 📌 (message_id=%d, chat_id=%d)\n", id, chatID)
			return nil
		},
	}
	pinCmd.Flags().BoolVar(&silent, "silent", false, "Pin without notifying the chat")
	pinCmd.Flags().BoolVar(&forSelf, "for-me", false, "Pin only for you (private chats only)")
	tgCmd.AddCommand(pinCmd)
}
