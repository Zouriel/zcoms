package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

// `zc locations …` is a thin alias forwarding to the agent tier (agent.sock), which
// owns this state in agent.db. Install the agent with `zc install agent`.
func init() {
	c := &cobra.Command{
		Use:                "locations [command…]",
		Short:              "Alias of `zc agent workspace` — the agent workspace registry",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return forwardToAgent(strings.TrimSpace("workspace " + strings.Join(args, " ")))
		},
	}
	rootCmd.AddCommand(c)
}
