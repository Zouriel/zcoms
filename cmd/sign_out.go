package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/config"
	"github.com/Zouriel/zcoms/internal/tdlib"

	"github.com/spf13/cobra"
)

func init() {
	var hardSignOut bool

	signOutCommand := &cobra.Command{
		Use:   "logout",
		Short: "Log out of Telegram",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireNoDaemon("logout"); err != nil {
				return err
			}
			message, hint, err := executeSignOut(hardSignOut)
			if err != nil {
				return err
			}
			if hint != "" {
				fmt.Println(hint)
			}
			fmt.Println(message)
			return nil
		},
	}

	signOutCommand.Flags().BoolVar(&hardSignOut, "hard", false, "Remove local Telegram session data (TDLib directory)")
	tgCmd.AddCommand(signOutCommand)
}

func executeSignOut(hardSignOut bool) (string, string, error) {
	tdjson, loadError := tdlib.LoadTDJSON()
	if loadError != nil {
		return "", "", loadError
	}
	defer tdjson.Close()

	tdlib.ConfigureLogging(tdjson)
	clientID := tdjson.CreateClientID()

	state, err := tdlib.FetchAuthorizationState(tdjson, clientID)
	if err != nil {
		if strings.Contains(err.Error(), "Initialization parameters are needed") {
			state = tdlib.AuthStateWaitTdlibParameters
		} else {
			return "", "", err
		}
	}

	if state == tdlib.AuthStateWaitTdlibParameters {
		apiID, apiHash, credErr := config.ResolveAPICredentials()
		if credErr != nil {
			return "", "", credErr
		}

		if err := tdlib.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
			return "", "", err
		}
	}

	_ = tdlib.LogOutSession(tdjson, clientID)

	waitError := waitUntilNotReady(tdjson, clientID, 5*time.Second)

	clearLocalSessionState()

	if saveError := config.Save(AppConfig, ConfigFilePath); saveError != nil {
		return "", "", saveError
	}

	if hardSignOut {
		if deleteError := removeTdlibDirWithRetry(AppConfig.TdlibDir, 8, 250*time.Millisecond); deleteError != nil {
			return "", "", deleteError
		}
		return "Logged out and TDLib data removed.", "", nil
	}

	if waitError != nil {
		return "Logged out.", "TDLib session still present. Try: logout --hard", nil
	}

	return "Logged out.", "", nil
}

func clearLocalSessionState() {
	AppConfig.AuthState = config.AuthStateUnauthorized
	AppConfig.Username = ""
	AppConfig.PhoneNumber = ""
}

func removeTdlibDirWithRetry(path string, attempts int, delay time.Duration) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := os.RemoveAll(path); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(delay)
	}
	return lastErr
}

func waitUntilNotReady(tdjson *tdlib.TDJSON, clientID int32, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		state, err := tdlib.FetchAuthorizationState(tdjson, clientID)
		if err == nil && state != tdlib.AuthStateReady {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for TDLib to leave Ready state")
}
