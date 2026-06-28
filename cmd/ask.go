package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/comms/telegram"

	"github.com/spf13/cobra"
)

func init() {
	askCommand := &cobra.Command{
		Use:   "ask @username message",
		Short: "Send a message and wait for the first reply",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			message := strings.Join(args[1:], " ")

			// Route through the bridge daemon if it's running (it owns the
			// session and will block for the user's reply, same as below).
			if handled, reply, err := daemonAsk(username, message); handled {
				if err != nil {
					return err
				}
				fmt.Println(reply)
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
			selfUser, err := telegram.FetchCurrentUser(tdjson, clientID)
			if err != nil {
				return err
			}

			userID, err := telegram.ResolveUserIdentifierByUsername(tdjson, clientID, username)
			if err != nil {
				return err
			}

			chatID, err := telegram.CreatePrivateChat(tdjson, clientID, userID)
			if err != nil {
				return err
			}

			if _, err := telegram.SendTextMessage(tdjson, clientID, chatID, message); err != nil {
				return err
			}

			for {
				updateJSON, err := telegram.ReceiveUpdates(tdjson)
				if err != nil || updateJSON == "" {
					continue
				}

				update, ok := telegram.ParseUpdateNewMessage(updateJSON)
				if !ok || update.Message.ChatID != chatID {
					continue
				}

				if update.Message.SenderID.Type == "messageSenderUser" && update.Message.SenderID.UserID == selfUser.ID {
					continue
				}

				switch update.Message.Content.Type {
				case "messageText":
					fmt.Println(update.Message.Content.Text.Text)
				default:
					fmt.Printf("[%s]\n", update.Message.Content.Type)
				}
				return nil
			}
		},
	}

	tgCmd.AddCommand(askCommand)
}
