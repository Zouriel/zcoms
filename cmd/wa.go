package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"zcoms/internal/agent"
	"zcoms/internal/whatsapp"

	"github.com/spf13/cobra"
)

// waJID normalizes a user-supplied recipient into a WhatsApp JID. A value that
// already contains "@" is used as-is (e.g. a full @s.whatsapp.net or @lid jid);
// otherwise the digits are kept and "@s.whatsapp.net" appended, so a plain phone
// number like "+960 798-8692" becomes "9607988692@s.whatsapp.net".
func waJID(to string) string {
	to = strings.TrimSpace(to)
	if strings.Contains(to, "@") {
		return to
	}
	var b strings.Builder
	for _, r := range to {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String() + "@s.whatsapp.net"
}

// waSocket returns the configured sidecar socket, erroring if WhatsApp is off.
func waSocket() (string, error) {
	s, _, err := agent.LoadOrSeedSettings()
	if err != nil {
		return "", err
	}
	if !s.WhatsApp.Enabled {
		return "", fmt.Errorf("WhatsApp is off — enable it in agent-settings.json (and run the sidecar)")
	}
	return s.WhatsApp.Socket, nil
}

func init() {
	waCommand := &cobra.Command{
		Use:   "wa",
		Short: "WhatsApp bridge (mirror of the Telegram commands, via the Baileys sidecar)",
		Long: "WhatsApp counterparts to the core Telegram commands, talking to the\n" +
			"Baileys sidecar over its unix socket (so they work regardless of the\n" +
			"Telegram daemon). Recipients accept a plain phone number or a full JID.",
	}

	statusCommand := &cobra.Command{
		Use:   "status",
		Short: "Ping the WhatsApp sidecar and report paired/connected state",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, _, err := agent.LoadOrSeedSettings()
			if err != nil {
				return err
			}

			enabled := "off"
			if s.WhatsApp.Enabled {
				enabled = "on"
			}
			fmt.Printf("WhatsApp: %s\n", enabled)
			fmt.Printf("Socket:   %s\n", s.WhatsApp.Socket)
			if !s.WhatsApp.Enabled {
				fmt.Println("(Enable it in agent-settings.json and run the sidecar to use it.)")
			}

			st, err := whatsapp.Ping(s.WhatsApp.Socket)
			if err != nil {
				fmt.Printf("Sidecar:  unreachable (%v)\n", err)
				return nil // not an error condition for a status probe
			}
			if st.Ready {
				fmt.Printf("Sidecar:  connected (paired, mirroring %d chat(s))\n", st.Chats)
			} else {
				fmt.Println("Sidecar:  running but not paired — run `zc wa login` to link a device")
			}
			return nil
		},
	}

	var loginWait time.Duration
	loginCommand := &cobra.Command{
		Use:   "login",
		Short: "Link WhatsApp by scanning a QR (counterpart to `zc tg login`)",
		Long: "Shows the pairing QR from the running sidecar. Scan it in WhatsApp →\n" +
			"Settings → Linked devices → Link a device. Requires the wa-bridge sidecar\n" +
			"to be running (systemctl --user start wa-bridge).",
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, err := waSocket()
			if err != nil {
				return err
			}
			ready, qr, err := whatsapp.QR(sock)
			if err != nil {
				return fmt.Errorf("sidecar unreachable (%v)\nstart it with: systemctl --user start wa-bridge", err)
			}
			if ready {
				fmt.Println("✅ Already linked to WhatsApp.")
				return nil
			}
			if qr == "" {
				fmt.Println("Sidecar is up but has no QR yet — it may still be starting.")
				fmt.Println("Try again in a few seconds, or watch: journalctl --user -u wa-bridge -f")
				return nil
			}
			fmt.Println("Scan this with WhatsApp → Settings → Linked devices → Link a device:")
			fmt.Println()
			fmt.Println(qr)

			// Poll until linked (or the wait elapses) so the command confirms success.
			deadline := time.Now().Add(loginWait)
			for time.Now().Before(deadline) {
				time.Sleep(2 * time.Second)
				if st, err := whatsapp.Ping(sock); err == nil && st.Ready {
					fmt.Println("✅ Linked! WhatsApp is now connected.")
					return nil
				}
			}
			fmt.Println("Still waiting — run `zc wa status` after scanning to confirm.")
			return nil
		},
	}
	loginCommand.Flags().DurationVar(&loginWait, "wait", 90*time.Second, "How long to wait for the scan before exiting")

	sendCommand := &cobra.Command{
		Use:   "send <number|jid> <message>",
		Short: "Send a WhatsApp message (counterpart to `zc tg send`)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, err := waSocket()
			if err != nil {
				return err
			}
			jid := waJID(args[0])
			if err := whatsapp.Send(sock, jid, strings.Join(args[1:], " ")); err != nil {
				return err
			}
			fmt.Printf("Message sent ✅ (%s)\n", jid)
			return nil
		},
	}

	sendFileCommand := &cobra.Command{
		Use:   "send-file <number|jid> <path> [caption]",
		Short: "Send a file on WhatsApp (counterpart to `zc tg send-file`)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, err := waSocket()
			if err != nil {
				return err
			}
			jid := waJID(args[0])
			path := expandUserPath(args[1])
			caption := strings.Join(args[2:], " ")
			if err := whatsapp.SendFile(sock, jid, path, caption); err != nil {
				return err
			}
			fmt.Printf("Sent %s ✅ (%s)\n", path, jid)
			return nil
		},
	}

	var unreadJSON bool
	unreadCommand := &cobra.Command{
		Use:   "unread",
		Short: "List unread WhatsApp 1:1 messages the sidecar has mirrored (read view)",
		RunE: func(cmd *cobra.Command, args []string) error {
			sock, err := waSocket()
			if err != nil {
				return err
			}
			msgs, err := whatsapp.FetchUnread(sock)
			if err != nil {
				return err
			}
			if unreadJSON {
				for _, m := range msgs {
					b, _ := json.Marshal(struct {
						ChatID string `json:"chat_id"`
						Sender string `json:"sender"`
						Text   string `json:"text"`
						File   string `json:"file,omitempty"`
						MsgID  string `json:"msg_id"`
					}{m.ChatID, m.Sender, m.Text, m.File, m.MsgID})
					fmt.Println(string(b))
				}
				return nil
			}
			if len(msgs) == 0 {
				fmt.Println("No unread WhatsApp messages.")
				return nil
			}
			for _, m := range msgs {
				fmt.Printf("%s [%s]: %s\n", m.Sender, m.ChatID, m.Text)
				if m.File != "" {
					fmt.Printf("   [file: %s]\n", m.File)
				}
			}
			return nil
		},
	}
	unreadCommand.Flags().BoolVar(&unreadJSON, "json", false, "Emit messages as JSON lines")

	waCommand.AddCommand(statusCommand, loginCommand, sendCommand, sendFileCommand, unreadCommand)
	rootCmd.AddCommand(waCommand)
}
