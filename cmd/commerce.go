package cmd

import (
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/client"

	"github.com/spf13/cobra"
)

func init() {
	commerceCommand := &cobra.Command{
		Use:   "commerce [command…]",
		Short: "Telegram-Stars commerce control plane (zcoms-commerce module)",
		Long: "Drives the zcoms-commerce module (store provisioning, products, orders,\n" +
			"refunds, billing, reporting) — the control plane for the Zcoms Commerce\n" +
			"platform. The merchant bots and payments live in the separate VPS runtime;\n" +
			"this administers it. Examples:\n" +
			"  zc commerce status\n" +
			"  zc commerce new store\n" +
			"  zc commerce store list\n" +
			"  zc commerce refund approve <id>\n" +
			"  zc commerce report platform\n" +
			"  zc commerce help",
		DisableFlagParsing: true, // pass the whole line through to the module
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.TrimSpace(strings.Join(args, " "))
			actor := strings.TrimSpace(AppConfig.Username)
			if actor != "" && !strings.HasPrefix(actor, "@") {
				actor = "@" + actor
			}
			res, err := client.ModuleCommand("commerce.sock", text, actor)
			if !res.Running {
				return fmt.Errorf("the commerce module isn't running — install it with `zc install commerce`")
			}
			if err != nil {
				return err
			}
			fmt.Println(res.Reply)
			return nil
		},
	}
	rootCmd.AddCommand(commerceCommand)
}
