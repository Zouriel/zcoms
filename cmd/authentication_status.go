package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/config"

	"github.com/spf13/cobra"
)

func init() {
	authenticationStatusCommand := &cobra.Command{
		Use:   "auth",
		Short: "Show authentication status",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireNoDaemon("auth"); err != nil {
				return err
			}
			tdjson, err := telegram.LoadTDJSON()
			if err != nil {
				return err
			}
			defer tdjson.Close()

			telegram.ConfigureLogging(tdjson)
			clientID := tdjson.CreateClientID()

			state, err := telegram.FetchAuthorizationState(tdjson, clientID)
			if err != nil {
				if !strings.Contains(err.Error(), "Initialization parameters are needed") {
					return err
				}

				apiID, apiHash, credErr := config.ResolveAPICredentials()
				if credErr != nil {
					if errors.Is(credErr, config.ErrMissingCredentials) {
						fmt.Println("Not logged in")
						return nil
					}
					return credErr
				}

				if err := telegram.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
					return err
				}

				state, err = telegram.FetchAuthorizationState(tdjson, clientID)
				if err != nil {
					return err
				}
			}

			if state == telegram.AuthStateWaitTdlibParameters {
				apiID, apiHash, credErr := config.ResolveAPICredentials()
				if credErr != nil {
					if errors.Is(credErr, config.ErrMissingCredentials) {
						fmt.Println("Not logged in")
						return nil
					}
					return credErr
				}

				if err := telegram.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
					return err
				}

				state, err = telegram.FetchAuthorizationState(tdjson, clientID)
				if err != nil {
					return err
				}
			}

			if state == telegram.AuthStateReady {
				user, err := telegram.FetchCurrentUser(tdjson, clientID)
				if err != nil {
					return err
				}

				updatedConfig, err := telegram.PersistIdentity(AppConfig, ConfigFilePath, user)
				if err != nil {
					return err
				}

				AppConfig = updatedConfig
				fmt.Println("Logged in as", telegram.BuildIdentityLabelFromUser(user))
				return nil
			}

			fmt.Println("Not logged in")
			return nil
		},
	}

	tgCmd.AddCommand(authenticationStatusCommand)
}
