package cmd

import (
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"

	"github.com/spf13/cobra"
)

func init() {
	tgCmd.AddCommand(&cobra.Command{
		Use:   "edit <@username|contact|group|chat id> <message id|last> <new text>",
		Short: "Edit a Telegram message you sent",
		Long: "Replace the text of a message you sent. Pass the message id that\n" +
			"`zc tg send` printed (or from `zc tg chat <chat> --read N --json`),\n" +
			"or \"last\" for the newest message you sent in that chat.\n" +
			"Text messages only; Telegram limits how long after sending you can edit.",
		SilenceUsage: true,
		Args:         cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			msgID, err := parseMessageRef(args[1])
			if err != nil {
				return err
			}
			text := strings.Join(args[2:], " ")
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("new text is empty")
			}
			chatID, id, err := runMessageOp(target, msgID,
				func(c *client.Client) (client.Response, error) { return c.Edit(target, msgID, text) },
				func(td *telegram.TDJSON, cid int32, chatID, id int64) error {
					return telegram.EditMessageText(td, cid, chatID, id, text)
				})
			if err != nil {
				return err
			}
			fmt.Printf("Message edited ✅ (message_id=%d, chat_id=%d)\n", id, chatID)
			return nil
		},
	})
}
