package cmd

import (
	"fmt"
	"time"

	"github.com/Zouriel/zcoms/client"
)

// These shims preserve the "route through the running comms daemon, else talk to
// Telegram directly" pattern the tg commands use. handled is false when no
// daemon is listening, so the caller falls back to its own TDLib session.

func daemonClient() (*client.Client, bool) {
	c, err := client.NewDefault()
	if err != nil {
		return nil, false
	}
	return c, c.Available()
}

func daemonRunning() bool {
	_, ok := daemonClient()
	return ok
}

func daemonSend(to, text, thread string) (handled bool, msgID, chatID int64, err error) {
	c, ok := daemonClient()
	if !ok {
		return false, 0, 0, nil
	}
	if thread != "" {
		resp, err := c.SendWith("telegram", to, text, client.SendOptions{Thread: thread})
		return true, resp.MessageID, resp.ChatID, err
	}
	resp, err := c.Send(to, text)
	return true, resp.MessageID, resp.ChatID, err
}

func daemonSendFile(to, path, caption, thread string) (handled bool, label string, chatID int64, err error) {
	c, ok := daemonClient()
	if !ok {
		return false, "", 0, nil
	}
	if thread != "" {
		resp, err := c.SendFileWith("telegram", to, path, caption, client.SendOptions{Thread: thread})
		return true, resp.Label, resp.ChatID, err
	}
	resp, err := c.SendFile(to, path, caption)
	return true, resp.Label, resp.ChatID, err
}

// errThreadNeedsDaemon is returned when --thread is used without the daemon:
// thread names are resolved there.
var errThreadNeedsDaemon = fmt.Errorf("--thread needs the comms daemon running")

func daemonAsk(to, text string) (handled bool, reply string, err error) {
	c, ok := daemonClient()
	if !ok {
		return false, "", nil
	}
	reply, err = c.Ask(to, text)
	return true, reply, err
}

func daemonRead(to string, count int, download bool) (handled bool, msgs []client.Message, err error) {
	c, ok := daemonClient()
	if !ok {
		return false, nil, nil
	}
	resp, err := c.Read(to, count, download)
	return true, resp.Messages, err
}

func daemonChatWait(to, text string, timeout time.Duration) (handled bool, reply string, err error) {
	c, ok := daemonClient()
	if !ok {
		return false, "", nil
	}
	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	resp, err := c.Do(client.Request{Op: "ask", To: to, Text: text}, deadline)
	if err != nil {
		return true, "", fmt.Errorf("%w", err)
	}
	return true, resp.Reply, nil
}
