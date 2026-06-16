package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"zcoms/internal/agent"
	"zcoms/internal/tdlib"

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
				state, stateErr := tdlib.FetchAuthorizationState(tdjson, clientID)
				if stateErr != nil {
					if strings.Contains(stateErr.Error(), "Initialization parameters are needed") ||
						strings.Contains(stateErr.Error(), "Request aborted") {
						if err := tdlib.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
							return err
						}
						time.Sleep(200 * time.Millisecond)
						continue
					}
					return stateErr
				}

				switch state {
				case tdlib.AuthStateWaitTdlibParameters:
					if err := tdlib.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
						return err
					}

				case tdlib.AuthStateWaitPhoneNumber:
					phone, err := promptLine(consoleReader, "Phone number (+countrycode...): ")
					if err != nil {
						return err
					}
					if err := tdlib.ProvideAuthenticationPhoneNumber(tdjson, clientID, phone); err != nil {
						return err
					}

				case tdlib.AuthStateWaitCode:
					code, err := promptLine(consoleReader, "Telegram code: ")
					if err != nil {
						return err
					}
					if err := tdlib.SubmitAuthenticationCode(tdjson, clientID, code); err != nil {
						return err
					}

				case tdlib.AuthStateWaitPassword:
					pass, err := promptHidden("2FA password: ")
					if err != nil {
						return err
					}
					if err := tdlib.SubmitAuthenticationPassword(tdjson, clientID, pass); err != nil {
						return err
					}

				case tdlib.AuthStateReady:
					goto AUTHED

				default:
				}

				time.Sleep(250 * time.Millisecond)
			}

		AUTHED:
			userID, err := tdlib.ResolveUserIdentifierByUsername(tdjson, clientID, username)
			if err != nil {
				return err
			}

			chatID, err := tdlib.CreatePrivateChat(tdjson, clientID, userID)
			if err != nil {
				return err
			}

			msgID, err := tdlib.SendTextMessage(tdjson, clientID, chatID, message)
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
