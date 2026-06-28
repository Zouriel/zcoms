package cmd

import (
	"fmt"

	"github.com/Zouriel/zcoms/internal/agent"
)

// requireNoDaemon fails fast with a clear message when the bridge daemon is
// running, since these commands open their own Telegram session and can't share
// the daemon's single locked one.
func requireNoDaemon(command string) error {
	if agent.DaemonRunning() {
		return fmt.Errorf("the tg daemon is running and owns the Telegram session, so `%s` can't run.\n"+
			"Stop it first:  systemctl --user stop zcoms-daemon", command)
	}
	return nil
}
