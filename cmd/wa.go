package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// waJID normalizes a phone number or JID into a WhatsApp JID. A value that
// already contains "@" is used as-is (a full @s.whatsapp.net or @lid jid);
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

// looksLikeWANumber reports whether s is a bare phone number / jid (digits, +,
// spaces, dashes, or an @) rather than a contact name to resolve.
func looksLikeWANumber(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "@") {
		return true
	}
	for _, r := range s {
		if (r < '0' || r > '9') && r != '+' && r != ' ' && r != '-' && r != '(' && r != ')' {
			return false
		}
	}
	return true
}

// resolveWA turns a recipient (phone number, JID, or a contact name) into a
// WhatsApp JID. Names are looked up in the contacts directory and resolved to
// the contact's WhatsApp address (its number, or an explicit wa id).
func resolveWA(to string) (string, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return "", fmt.Errorf("a recipient (number, jid, or contact name) is required")
	}
	if looksLikeWANumber(to) {
		return waJID(to), nil
	}
	c, ok := daemonClient()
	if !ok {
		return "", fmt.Errorf("the comms daemon isn't running — needed to resolve a contact name (or pass a number)")
	}
	matches, err := c.ResolveContact(to)
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no contact matching %q (try a phone number)", to)
	}
	addr := matches[0].Address("whatsapp")
	if addr == "" {
		return "", fmt.Errorf("contact %q has no WhatsApp number", matches[0].Name)
	}
	return waJID(addr), nil
}

func init() {
	waCommand := &cobra.Command{
		Use:   "wa",
		Short: "WhatsApp (in-process whatsmeow, via the comms daemon)",
		Long: "WhatsApp counterparts to the core Telegram commands. WhatsApp now runs\n" +
			"in-process (whatsmeow) inside the comms daemon, so these route through it\n" +
			"(the Node Baileys sidecar is retired). Recipients accept a phone number, a\n" +
			"full JID, or a contact name. The daemon must be running.",
	}

	statusCommand := &cobra.Command{
		Use:   "status",
		Short: "Report WhatsApp connection state (from the daemon's connectors)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := daemonClient()
			if !ok {
				fmt.Println("WhatsApp: daemon offline — start it (zc init agent / the systemd service)")
				return nil
			}
			conns, err := c.Connectors()
			if err != nil {
				return err
			}
			for _, conn := range conns {
				if conn.Transport == "whatsapp" {
					line := conn.State
					if conn.Detail != "" {
						line += " (" + conn.Detail + ")"
					}
					fmt.Printf("WhatsApp: %s\n", line)
					if conn.State == "action_required" {
						fmt.Println("Pair it on the console Connectors page (scan the QR).")
					}
					return nil
				}
			}
			fmt.Println("WhatsApp: not registered on the daemon")
			return nil
		},
	}

	loginCommand := &cobra.Command{
		Use:   "login",
		Short: "How to pair WhatsApp (QR is on the console Connectors page)",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("WhatsApp pairing is now the in-process whatsmeow QR, shown in the console:")
			fmt.Println("  open the console → Connectors → WhatsApp → Pair, then scan the QR")
			fmt.Println("  (WhatsApp → Settings → Linked devices → Link a device).")
			fmt.Println("Check state any time with: zc wa status")
			return nil
		},
	}

	sendCommand := &cobra.Command{
		Use:   "send <number|jid|contact> <message>",
		Short: "Send a WhatsApp message",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := daemonClient()
			if !ok {
				return fmt.Errorf("the comms daemon isn't running")
			}
			jid, err := resolveWA(args[0])
			if err != nil {
				return err
			}
			if _, err := c.SendOn("whatsapp", jid, strings.Join(args[1:], " ")); err != nil {
				return err
			}
			fmt.Printf("Message sent ✅ (%s)\n", jid)
			return nil
		},
	}

	sendFileCommand := &cobra.Command{
		Use:   "send-file <number|jid|contact> <path> [caption]",
		Short: "Send a file on WhatsApp",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := daemonClient()
			if !ok {
				return fmt.Errorf("the comms daemon isn't running")
			}
			jid, err := resolveWA(args[0])
			if err != nil {
				return err
			}
			path := expandUserPath(args[1])
			caption := strings.Join(args[2:], " ")
			if _, err := c.SendFileOn("whatsapp", jid, path, caption); err != nil {
				return err
			}
			fmt.Printf("Sent %s ✅ (%s)\n", path, jid)
			return nil
		},
	}

	var readCount int
	readCommand := &cobra.Command{
		Use:   "read <number|jid|contact> [count]",
		Short: "Show recent WhatsApp history with a contact (from the daemon's store)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := daemonClient()
			if !ok {
				return fmt.Errorf("the comms daemon isn't running")
			}
			jid, err := resolveWA(args[0])
			if err != nil {
				return err
			}
			count := readCount
			if len(args) > 1 {
				if n, e := strconv.Atoi(args[1]); e == nil && n > 0 {
					count = n
				}
			}
			resp, err := c.ReadOn("whatsapp", jid, count)
			if err != nil {
				return err
			}
			if len(resp.Messages) == 0 {
				fmt.Printf("No stored history with %s yet.\n", jid)
				fmt.Println("(History is kept from when whatsmeow started — older messages aren't stored.)")
				return nil
			}
			for _, m := range resp.Messages { // oldest-first
				who := m.Sender
				if m.Outgoing {
					who = "you"
				}
				body := m.Text
				if m.Kind != "messageText" && m.Kind != "text" {
					if m.File != "" {
						body = "[" + m.Kind + "] " + m.File
					} else if body == "" {
						body = "[" + m.Kind + "]"
					}
				}
				fmt.Printf("[%s] %s: %s\n", time.Unix(m.Date, 0).Format("Jan 02 15:04"), who, body)
			}
			return nil
		},
	}
	readCommand.Flags().IntVar(&readCount, "count", 10, "How many recent messages to show")

	var unreadJSON bool
	unreadCommand := &cobra.Command{
		Use:   "unread",
		Short: "List unread WhatsApp 1:1 messages (from the daemon's store)",
		RunE: func(cmd *cobra.Command, args []string) error {
			c, ok := daemonClient()
			if !ok {
				return fmt.Errorf("the comms daemon isn't running")
			}
			items, err := c.Unread()
			if err != nil {
				return err
			}
			var wa []struct {
				Chat, Sender, Text, File, Ref string
			}
			for _, it := range items {
				if it.Transport != "whatsapp" {
					continue
				}
				wa = append(wa, struct{ Chat, Sender, Text, File, Ref string }{it.Address, it.Sender, it.Text, it.File, it.MsgRef})
			}
			if unreadJSON {
				for _, m := range wa {
					b, _ := json.Marshal(struct {
						ChatID string `json:"chat_id"`
						Sender string `json:"sender"`
						Text   string `json:"text"`
						File   string `json:"file,omitempty"`
						MsgID  string `json:"msg_id"`
					}{m.Chat, m.Sender, m.Text, m.File, m.Ref})
					fmt.Println(string(b))
				}
				return nil
			}
			if len(wa) == 0 {
				fmt.Println("No unread WhatsApp messages.")
				return nil
			}
			for _, m := range wa {
				fmt.Printf("%s [%s]: %s\n", m.Sender, m.Chat, m.Text)
				if m.File != "" {
					fmt.Printf("   [file: %s]\n", m.File)
				}
			}
			return nil
		},
	}
	unreadCommand.Flags().BoolVar(&unreadJSON, "json", false, "Emit messages as JSON lines")

	waCommand.AddCommand(statusCommand, loginCommand, sendCommand, sendFileCommand, readCommand, unreadCommand)
	rootCmd.AddCommand(waCommand)
}
