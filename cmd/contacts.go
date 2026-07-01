package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"

	"github.com/spf13/cobra"
)

// openContacts opens the comms-owned contacts directory (comms.db). `zc` is the
// comms binary, so the CLI touches the store directly (same tier); the agent and
// modules reach it through the daemon over comms/client instead.
func openContacts() (*contacts.Store, error) {
	dir, err := client.DefaultAppDir()
	if err != nil {
		return nil, err
	}
	return contacts.Open(filepath.Join(dir, "comms.db"))
}

// channelFlags registers the per-channel fields shared by `add` and `edit` and
// returns accessors. Phone is the universal number (Telegram/WhatsApp/Viber);
// Discord needs its own id. Discord/Viber are stored for the future.
func channelFlags(c *cobra.Command, t *client.Contact) {
	c.Flags().StringSliceVar(&t.Aliases, "alias", nil, "Alternate name (repeatable or comma-separated; unique across all names+aliases)")
	c.Flags().StringVar(&t.Phone, "phone", "", "Mobile number (reaches Telegram/WhatsApp/Viber)")
	c.Flags().StringVar(&t.Email, "email", "", "Email address")
	c.Flags().StringVar(&t.Telegram, "telegram", "", "Telegram @handle (falls back to --phone)")
	c.Flags().StringVar(&t.WhatsApp, "whatsapp", "", "WhatsApp id/number (falls back to --phone)")
	c.Flags().StringVar(&t.Instagram, "instagram", "", "Instagram @handle (no phone fallback; future)")
	c.Flags().StringVar(&t.Discord, "discord", "", "Discord id (no phone fallback; future)")
	c.Flags().StringVar(&t.Viber, "viber", "", "Viber id (falls back to --phone; future)")
	c.Flags().StringVar(&t.Github, "github", "", "GitHub handle (contact info)")
	c.Flags().StringVar(&t.Note, "note", "", "A free-text note")
}

func init() {
	contactsCmd := &cobra.Command{
		Use:   "contacts",
		Short: "Manage the contacts directory (people + their per-channel addresses)",
		Long: "The comms-owned address book (comms.db). Each contact has explicit channels:\n" +
			"a phone number (which reaches Telegram, WhatsApp and Viber), an email, and\n" +
			"per-platform ids (telegram/whatsapp/discord/viber) that override the phone.\n" +
			"Discord needs its own id — a phone can't reach it. Every tier resolves\n" +
			"\"message <name> on whatever channel\" through here.",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all contacts and their channels",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openContacts()
			if err != nil {
				return err
			}
			defer s.Close()
			cs, err := s.List()
			if err != nil {
				return err
			}
			if len(cs) == 0 {
				fmt.Println("No contacts yet. Add one with `zc contacts add <name> --phone +960… --telegram @x`.")
				return nil
			}
			for _, c := range cs {
				fmt.Printf("#%d  %s\n", c.ID, c.Name)
				if len(c.Aliases) > 0 {
					fmt.Printf("      %-10s %s\n", "aliases:", strings.Join(c.Aliases, ", "))
				}
				for _, f := range []struct{ label, val string }{
					{"phone", c.Phone},
					{"email", c.Email},
					{"telegram", c.Telegram},
					{"whatsapp", c.WhatsApp},
					{"instagram", c.Instagram},
					{"discord", c.Discord},
					{"viber", c.Viber},
					{"github", c.Github},
					{"note", c.Note},
				} {
					if f.val != "" {
						fmt.Printf("      %-10s %s\n", f.label+":", f.val)
					}
				}
			}
			return nil
		},
	}

	var addFields client.Contact
	addCmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a contact",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openContacts()
			if err != nil {
				return err
			}
			defer s.Close()
			addFields.Name = strings.Join(args, " ")
			c, err := s.Create(contacts.Owner, addFields)
			if err != nil {
				return err
			}
			fmt.Printf("Added #%d %s ✅\n", c.ID, c.Name)
			return nil
		},
	}
	channelFlags(addCmd, &addFields)

	var editFields client.Contact
	editCmd := &cobra.Command{
		Use:   "edit <id> [name]",
		Short: "Edit a contact (only the flags you pass are changed)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("bad id %q", args[0])
			}
			s, err := openContacts()
			if err != nil {
				return err
			}
			defer s.Close()
			cur, err := s.Get(id)
			if err != nil {
				return err
			}
			if len(args) > 1 {
				cur.Name = strings.Join(args[1:], " ")
			}
			// Apply only the channel flags the user actually set, so an edit is a
			// patch over the existing row (the store does a full overwrite).
			for name, dst := range map[string]*string{
				"phone": &cur.Phone, "email": &cur.Email, "telegram": &cur.Telegram,
				"whatsapp": &cur.WhatsApp, "instagram": &cur.Instagram,
				"discord": &cur.Discord, "viber": &cur.Viber, "github": &cur.Github, "note": &cur.Note,
			} {
				if cmd.Flags().Changed(name) {
					v, _ := cmd.Flags().GetString(name)
					*dst = v
				}
			}
			// Aliases is a list flag — replace wholesale when --alias was passed.
			if cmd.Flags().Changed("alias") {
				cur.Aliases, _ = cmd.Flags().GetStringSlice("alias")
			}
			if err := s.Update(contacts.Owner, cur); err != nil {
				return err
			}
			fmt.Println("Updated ✅")
			return nil
		},
	}
	channelFlags(editCmd, &editFields)

	rmCmd := &cobra.Command{
		Use:   "rm <id>",
		Short: "Remove a contact",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("bad id %q", args[0])
			}
			s, err := openContacts()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.Delete(contacts.Owner, id); err != nil {
				return err
			}
			fmt.Println("Removed ✅")
			return nil
		},
	}

	contactsCmd.AddCommand(listCmd, addCmd, editCmd, rmCmd)
	rootCmd.AddCommand(contactsCmd)
}
