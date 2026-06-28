package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"
	"github.com/Zouriel/zcoms/internal/comms/hub"
	"github.com/Zouriel/zcoms/internal/comms/telegram"

	"github.com/spf13/cobra"
)

func init() {
	initCommand := &cobra.Command{
		Use:   "init",
		Short: "Start long-running zcoms services",
	}

	agentCommand := &cobra.Command{
		Use:   "agent",
		Short: "Run the comms daemon (owns the Telegram session; serves the IPC socket)",
		Long: "Owns the single TDLib Telegram session and serves it to the agent tier and\n" +
			"modules over the IPC socket: send/ask/read/unread/mark_read/resolve, the\n" +
			"contacts directory, and a subscribe stream of incoming 1:1 messages. The AI\n" +
			"agent itself runs in the separate `zcoms-agent` process — install it with\n" +
			"`zc install agent`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			apiID, apiHash, err := resolveTelegramCredentials()
			if err != nil {
				return err
			}

			dir, err := client.DefaultAppDir()
			if err != nil {
				return err
			}
			store, err := contacts.Open(filepath.Join(dir, "comms.db"))
			if err != nil {
				return fmt.Errorf("opening contacts store: %w", err)
			}
			defer store.Close()

			tdjson, clientID, err := startTDLibClient()
			if err != nil {
				return err
			}
			defer tdjson.Close()

			if err := waitUntilReady(tdjson, clientID, apiID, apiHash); err != nil {
				return err
			}

			self, err := telegram.FetchCurrentUser(tdjson, clientID)
			if err == nil {
				fmt.Println("Running as:", self.FirstName, self.LastName)
				// Record identity + auth_state so config.json reflects the live
				// session (the daemon owns TDLib, so `zc tg auth` can't run to do it).
				if updated, perr := telegram.PersistIdentity(AppConfig, ConfigFilePath, self); perr == nil {
					AppConfig = updated
				}
			}

			return hub.RunDaemon(tdjson, clientID, store)
		},
	}

	initCommand.AddCommand(agentCommand)
	rootCmd.AddCommand(initCommand)
}
