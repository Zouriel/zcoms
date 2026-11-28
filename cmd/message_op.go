package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
)

// parseMessageRef reads a message argument: a numeric message id, or "last"
// for the newest message you sent in the chat (returned as 0).
func parseMessageRef(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "last") {
		return 0, nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("message must be a message id or \"last\", got %q", s)
	}
	return id, nil
}

// runMessageOp acts on one message in target: through the daemon when it is
// running (viaDaemon), else on a TDLib session of its own (local, given the
// resolved chat and message ids). A zero msgID means the newest message you
// sent there. It returns the chat and message ids acted on.
func runMessageOp(
	target string,
	msgID int64,
	viaDaemon func(c *client.Client) (client.Response, error),
	local func(td *telegram.TDJSON, clientID int32, chatID, msgID int64) error,
) (chatID, actedID int64, err error) {
	if c, ok := daemonClient(); ok {
		resp, err := viaDaemon(c)
		return resp.ChatID, resp.MessageID, err
	}

	apiID, apiHash, err := resolveTelegramCredentials()
	if err != nil {
		return 0, 0, err
	}
	td, clientID, err := startTDLibClient()
	if err != nil {
		return 0, 0, err
	}
	defer td.Close()
	if err := waitUntilReady(td, clientID, apiID, apiHash); err != nil {
		return 0, 0, err
	}
	if chatID, err = resolveChatTarget(td, clientID, target); err != nil {
		return 0, 0, err
	}
	if msgID == 0 {
		if msgID, err = telegram.LastOutgoingID(td, clientID, chatID); err != nil {
			return chatID, 0, err
		}
	}
	return chatID, msgID, local(td, clientID, chatID, msgID)
}
