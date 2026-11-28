package cmd

import (
	"fmt"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"

	"github.com/spf13/cobra"
)

func init() {
	tgCmd.AddCommand(&cobra.Command{
		Use:   "delete <@username|contact|group|chat id> <message id|last>...",
		Short: "Delete Telegram messages for everyone",
		Long: "Delete messages by id, or \"last\" for the newest message you sent in\n" +
			"that chat. Deleting someone else's message needs the delete right in\n" +
			"that group. This cannot be undone.",
		Args:         cobra.MinimumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			var ids []int64
			for _, a := range args[1:] {
				id, err := parseMessageRef(a)
				if err != nil {
					return err
				}
				if id == 0 && len(args) > 2 {
					return fmt.Errorf("\"last\" deletes one message; give ids to delete several")
				}
				if id != 0 {
					ids = append(ids, id)
				}
			}
			var first int64
			if len(ids) > 0 {
				first = ids[0]
			}
			chatID, id, err := runMessageOp(target, first,
				func(c *client.Client) (client.Response, error) { return c.Delete(target, ids...) },
				func(td *telegram.TDJSON, cid int32, chatID, id int64) error {
					if len(ids) == 0 {
						return telegram.DeleteMessages(td, cid, chatID, []int64{id})
					}
					return telegram.DeleteMessages(td, cid, chatID, ids)
				})
			if err != nil {
				return err
			}
			if len(ids) > 1 {
				fmt.Printf("Deleted %d messages ✅ (chat_id=%d)\n", len(ids), chatID)
				return nil
			}
			fmt.Printf("Message deleted ✅ (message_id=%d, chat_id=%d)\n", id, chatID)
			return nil
		},
	})
}
