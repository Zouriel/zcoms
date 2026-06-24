package cmd

import (
	"fmt"
	"strings"

	"zcoms/internal/agent"

	"github.com/spf13/cobra"
)

func init() {
	commerceCommand := &cobra.Command{
		Use:   "commerce [command…]",
		Short: "Zcoms Commerce control plane: stores, products, billing, reports (zc-commerce component)",
		Long: "Drives the zc-commerce component (Zcoms Commerce control plane: store\n" +
			"provisioning, product management, billing administration, and reporting\n" +
			"against the runtime). Examples:\n" +
			"  zc commerce status\n" +
			"  zc commerce store list\n" +
			"  zc commerce report platform\n" +
			"  zc commerce help",
		DisableFlagParsing: true, // pass the whole line through to the component
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.TrimSpace(strings.Join(args, " "))
			actor := strings.TrimSpace(AppConfig.Username)
			if actor != "" && !strings.HasPrefix(actor, "@") {
				actor = "@" + actor
			}
			handled, reply, err := agent.ComponentCommand("commerce.sock", text, actor)
			if !handled {
				return fmt.Errorf("the commerce component isn't running — install it with `zc install commerce`")
			}
			if err != nil {
				return err
			}
			fmt.Println(reply)
			return nil
		},
	}
	rootCmd.AddCommand(commerceCommand)
}
