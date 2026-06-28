package cmd

import (
	"strings"

	"github.com/spf13/cobra"
)

// `zc agents …` is a thin alias forwarding to the agent tier (agent.sock), which
// owns this state in agent.db. Install the agent with `zc install agent`.
func init() {
	c := &cobra.Command{
		Use:                "agents [command…]",
		Short:              "Choose the AI backend (claude/codex) per persona (agent tier)",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return forwardToAgent(strings.TrimSpace("agents " + strings.Join(args, " ")))
		},
	}
	rootCmd.AddCommand(c)
}
