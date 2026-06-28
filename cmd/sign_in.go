package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/config"
	"github.com/Zouriel/zcoms/internal/tdlib"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func init() {
	signInCommand := &cobra.Command{
		Use:   "login",
		Short: "Sign in to Telegram using TDLib",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireNoDaemon("login"); err != nil {
				return err
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
			defer func() { _ = safeCloseTDJSON(&tdjson) }()

			didInteractiveAuth := false

			for {
				authorizationState, stateErr := tdlib.FetchAuthorizationState(tdjson, clientID)
				if stateErr != nil {
					if isRecoverableTDLibError(stateErr) {
						_ = safeCloseTDJSON(&tdjson)
						tdjson, clientID, err = startTDLibClient()
						if err != nil {
							return err
						}
						time.Sleep(300 * time.Millisecond)
						continue
					}
					return stateErr
				}

				switch authorizationState {

				case tdlib.AuthStateWaitTdlibParameters:
					if err := tdlib.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
						return err
					}

				case tdlib.AuthStateWaitPhoneNumber:
					didInteractiveAuth = true
					phoneNumber, err := promptLine(consoleReader, "Phone number (+countrycode...): ")
					if err != nil {
						return err
					}
					if phoneNumber == "" {
						fmt.Println("Phone number cannot be empty.")
						continue
					}
					if err := tdlib.ProvideAuthenticationPhoneNumber(tdjson, clientID, phoneNumber); err != nil {
						return err
					}

				case tdlib.AuthStateWaitCode:
					didInteractiveAuth = true
					code, err := promptLine(consoleReader, "Telegram code: ")
					if err != nil {
						return err
					}
					if code == "" {
						fmt.Println("Code cannot be empty.")
						continue
					}
					if err := tdlib.SubmitAuthenticationCode(tdjson, clientID, code); err != nil {
						return err
					}

				case tdlib.AuthStateWaitPassword:
					didInteractiveAuth = true
					password, err := promptHidden("2FA password: ")
					if err != nil {
						return err
					}
					if password == "" {
						fmt.Println("Password cannot be empty.")
						continue
					}
					if err := tdlib.SubmitAuthenticationPassword(tdjson, clientID, password); err != nil {
						return err
					}

				case tdlib.AuthStateReady:
					if didInteractiveAuth {
						fmt.Println("Logged in ✅")
						return nil
					}

					choice, err := promptLine(consoleReader, "Existing session found. Use it? [Y/n]: ")
					if err != nil {
						return err
					}
					if choice == "" || strings.EqualFold(choice, "y") || strings.EqualFold(choice, "yes") {
						fmt.Println("Logged in ✅")
						return nil
					}

					fmt.Println("Refused existing session. To login to a different account, use a different TDLib database directory.")
					return nil

				case tdlib.AuthStateLoggingOut, tdlib.AuthStateClosing, tdlib.AuthStateClosed:
					_ = safeCloseTDJSON(&tdjson)
					tdjson, clientID, err = startTDLibClient()
					if err != nil {
						return err
					}

				default:
				}

				time.Sleep(250 * time.Millisecond)
			}
		},
	}

	tgCmd.AddCommand(signInCommand)
}

func safeCloseTDJSON(tdjson **tdlib.TDJSON) error {
	if tdjson == nil || *tdjson == nil {
		return nil
	}
	err := (*tdjson).Close()
	*tdjson = nil
	return err
}

func isRecoverableTDLibError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Initialization parameters are needed") ||
		strings.Contains(msg, "Request aborted") ||
		strings.Contains(msg, "Not enough resources")
}

func startTDLibClient() (*tdlib.TDJSON, int32, error) {
	tdjson, err := tdlib.LoadTDJSON()
	if err != nil {
		return nil, 0, err
	}

	tdlib.ConfigureLogging(tdjson)

	clientID := tdjson.CreateClientID()

	return tdjson, clientID, nil
}

func resolveTelegramCredentials() (int32, string, error) {
	return config.ResolveAPICredentials()
}

func promptLine(consoleReader *bufio.Reader, prompt string) (string, error) {
	fmt.Print(prompt)
	line, err := consoleReader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func promptHidden(prompt string) (string, error) {
	fmt.Print(prompt)
	passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(passwordBytes)), nil
}
