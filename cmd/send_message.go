package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/agent"
	"github.com/Zouriel/zcoms/internal/comms/telegram"

	"github.com/spf13/cobra"
)

func init() {
	sendMessageCommand := &cobra.Command{
		Use:   "send @username message",
		Short: "Send a Telegram message to a user",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			message := strings.Join(args[1:], " ")

			// If the bridge daemon is running, route through it (it owns the
			// session); otherwise talk to Telegram directly.
			if handled, msgID, chatID, err := agent.DaemonSend(username, message); handled {
				if err != nil {
					return err
				}
				fmt.Printf("Message sent ✅ (message_id=%d, chat_id=%d)\n", msgID, chatID)
				return nil
			}

			apiID, apiHash, err := resolveTelegramCredentials()
			if err != nil {
				return err
			}

			consoleReader := bufio.NewReader(os.Stdin)

			tdjson, clientID, err := startTDLibClient()
			if err != nil {
				return err
			}
			defer tdjson.Close()

			for {
				state, stateErr := telegram.FetchAuthorizationState(tdjson, clientID)
				if stateErr != nil {
					if strings.Contains(stateErr.Error(), "Initialization parameters are needed") ||
						strings.Contains(stateErr.Error(), "Request aborted") {
						if err := telegram.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
							return err
						}
						time.Sleep(200 * time.Millisecond)
						continue
					}
					return stateErr
				}

				switch state {
				case telegram.AuthStateWaitTdlibParameters:
					if err := telegram.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
						return err
					}

				case telegram.AuthStateWaitPhoneNumber:
					phone, err := promptLine(consoleReader, "Phone number (+countrycode...): ")
					if err != nil {
						return err
					}
					if err := telegram.ProvideAuthenticationPhoneNumber(tdjson, clientID, phone); err != nil {
						return err
					}

				case telegram.AuthStateWaitCode:
					code, err := promptLine(consoleReader, "Telegram code: ")
					if err != nil {
						return err
					}
					if err := telegram.SubmitAuthenticationCode(tdjson, clientID, code); err != nil {
						return err
					}

				case telegram.AuthStateWaitPassword:
					pass, err := promptHidden("2FA password: ")
					if err != nil {
						return err
					}
					if err := telegram.SubmitAuthenticationPassword(tdjson, clientID, pass); err != nil {
						return err
					}

				case telegram.AuthStateReady:
					goto AUTHED

				default:
				}

				time.Sleep(250 * time.Millisecond)
			}

		AUTHED:
			userID, err := telegram.ResolveUserIdentifierByUsername(tdjson, clientID, username)
			if err != nil {
				return err
			}

			chatID, err := telegram.CreatePrivateChat(tdjson, clientID, userID)
			if err != nil {
				return err
			}

			msgID, err := telegram.SendTextMessage(tdjson, clientID, chatID, message)
			if err != nil {
				return err
			}
			fmt.Printf("Message sent ✅ (message_id=%d, chat_id=%d)\n", msgID, chatID)

			time.Sleep(800 * time.Millisecond)
			return nil

		},
	}

	tgCmd.AddCommand(sendMessageCommand)
}
