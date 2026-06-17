package cmd

import (
	"fmt"
	"strings"

	"zcoms/internal/agent"

	"github.com/spf13/cobra"
)

func init() {
	teamCommand := &cobra.Command{
		Use:   "team [command…]",
		Short: "Team coordination: delegators, staff, tasks, standups (zc-team component)",
		Long: "Drives the zc-team component (team coordination, task delegation, GitHub\n" +
			"Projects sync, automated standups). Examples:\n" +
			"  zc team delegator create hems-dev\n" +
			"  zc team staff add hems-dev @ali staff 2\n" +
			"  zc team standup create hems-morning hems-dev @hems_team 09:00 Indian/Maldives\n" +
			"  zc team help",
		DisableFlagParsing: true, // pass the whole line through to the component
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			text := strings.TrimSpace(strings.Join(args, " "))
			actor := strings.TrimSpace(AppConfig.Username)
			if actor != "" && !strings.HasPrefix(actor, "@") {
				actor = "@" + actor
			}
			handled, reply, err := agent.ComponentCommand("team.sock", text, actor)
			if !handled {
				return fmt.Errorf("the team component isn't running — install it with `zc install team`")
			}
			if err != nil {
				return err
			}
			fmt.Println(reply)
			return nil
		},
	}
	rootCmd.AddCommand(teamCommand)
}
