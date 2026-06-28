package cmd

import (
	"fmt"

	"github.com/Zouriel/zcoms/internal/agent"
	"github.com/Zouriel/zcoms/internal/authentication"
	"github.com/Zouriel/zcoms/internal/tdlib"

	"github.com/spf13/cobra"
)

func init() {
	initCommand := &cobra.Command{
		Use:   "init",
		Short: "Start long-running zcoms services",
	}

	agentCommand := &cobra.Command{
		Use:   "agent",
		Short: "Run the agent bridge: let allow-listed Telegram users drive AI agent sessions",
		Long: "Listens on the logged-in Telegram account for messages from allow-listed\n" +
			"users and drives an AI agent (Claude Code or Codex) on their behalf: pick a\n" +
			"location, resume a session with a summary, chat back and forth. Configure with\n" +
			"agent-locations.json and agent-allowlist.json in the zcoms config dir.",
		RunE: func(cmd *cobra.Command, args []string) error {
			apiID, apiHash, err := resolveTelegramCredentials()
			if err != nil {
				return err
			}

			locations, locPath, err := agent.LoadOrSeedLocations()
			if err != nil {
				return err
			}
			allow, allowPath, err := agent.LoadOrSeedAllowlist()
			if err != nil {
				return err
			}
			settings, settingsPath, err := agent.LoadOrSeedSettings()
			if err != nil {
				return err
			}
			agents, agentsPath, err := agent.LoadOrSeedAgents()
			if err != nil {
				return err
			}

			fmt.Println("locations:", locPath)
			fmt.Println("allowlist:", allowPath)
			fmt.Println("settings: ", settingsPath)
			fmt.Println("agents:   ", agentsPath)

			tdjson, clientID, err := startTDLibClient()
			if err != nil {
				return err
			}
			defer tdjson.Close()

			if err := waitUntilReady(tdjson, clientID, apiID, apiHash); err != nil {
				return err
			}

			self, err := tdlib.FetchCurrentUser(tdjson, clientID)
			if err == nil {
				fmt.Println("Running as:", self.FirstName, self.LastName)
				// Record identity + auth_state so config.json reflects the live
				// session (the daemon owns TDLib, so `zc tg auth` can't run to do it).
				if updated, perr := authentication.PersistIdentity(AppConfig, ConfigFilePath, self); perr == nil {
					AppConfig = updated
				}
			}

			return agent.RunDaemon(tdjson, clientID, locations, allow, settings, agents)
		},
	}

	initCommand.AddCommand(agentCommand)
	rootCmd.AddCommand(initCommand)
}
