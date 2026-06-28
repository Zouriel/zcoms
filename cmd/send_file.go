package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/agent"
	"github.com/Zouriel/zcoms/internal/comms/telegram"

	"github.com/spf13/cobra"
)

func init() {
	sendFileCommand := &cobra.Command{
		Use:   "send-file <@username|chat_id> <path> [caption]",
		Short: "Send a file (photo/video/audio/document) to a chat",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := strings.TrimSpace(args[0])
			path := expandUserPath(args[1])
			caption := strings.Join(args[2:], " ")

			// Route through the daemon if it's running (it owns the session).
			if handled, label, chatID, err := agent.DaemonSendFile(target, path, caption); handled {
				if err != nil {
					return err
				}
				fmt.Printf("Sent %s ✅ (chat_id=%d)\n", label, chatID)
				return nil
			}

			apiID, apiHash, err := resolveTelegramCredentials()
			if err != nil {
				return err
			}

			tdjson, clientID, err := startTDLibClient()
			if err != nil {
				return err
			}
			defer tdjson.Close()

			if err := waitUntilReady(tdjson, clientID, apiID, apiHash); err != nil {
				return err
			}

			chatID, err := resolveChatTarget(tdjson, clientID, target)
			if err != nil {
				return err
			}

			temporaryMessageID, label, err := telegram.SendLocalFileMessage(tdjson, clientID, chatID, path, caption)
			if err != nil {
				return err
			}

			fmt.Printf("Uploading %s...\n", label)
			if err := telegram.WaitForSendCompletion(tdjson, clientID, temporaryMessageID, 30*time.Minute); err != nil {
				return err
			}

			fmt.Printf("Sent %s ✅ (chat_id=%d)\n", label, chatID)
			return nil
		},
	}

	tgCmd.AddCommand(sendFileCommand)
}

// waitUntilReady drives the auth state machine far enough to reach Ready,
// applying TDLib parameters if asked. It assumes an existing session (no
// interactive login) — run `zc tg login` first if not authenticated.
func waitUntilReady(tdjson *telegram.TDJSON, clientID int32, apiID int32, apiHash string) error {
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		state, err := telegram.FetchAuthorizationState(tdjson, clientID)
		if err != nil {
			if strings.Contains(err.Error(), "Initialization parameters are needed") ||
				strings.Contains(err.Error(), "Request aborted") {
				if err := telegram.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
					return err
				}
				time.Sleep(200 * time.Millisecond)
				continue
			}
			return err
		}

		switch state {
		case telegram.AuthStateReady:
			return nil
		case telegram.AuthStateWaitTdlibParameters:
			if err := telegram.ApplyTdlibParameters(tdjson, clientID, AppConfig.TdlibDir, apiID, apiHash); err != nil {
				return err
			}
		default:
			return fmt.Errorf("not logged in (run `zc tg login`)")
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for authorization")
}

// resolveChatTarget turns a numeric chat id or @username into a chat id.
func resolveChatTarget(tdjson *telegram.TDJSON, clientID int32, target string) (int64, error) {
	if id, err := strconv.ParseInt(target, 10, 64); err == nil {
		return id, nil
	}
	username := strings.TrimPrefix(target, "@")
	return telegram.ResolveChatIdentifierByUsername(tdjson, clientID, username)
}
