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

func init() {
	contactsCmd := &cobra.Command{
		Use:   "contacts",
		Short: "Manage the contacts directory (people + their per-platform handles)",
		Long: "The comms-owned address book (comms.db): a person can have several handles\n" +
			"(Ali = @ali on Telegram, +960… on WhatsApp). Every tier resolves \"message Ali\n" +
			"on whatever channel\" through here.",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List all contacts and their handles",
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
				fmt.Println("No contacts yet. Add one with `zc contacts add <name>`.")
				return nil
			}
			for _, c := range cs {
				fmt.Printf("#%d  %s", c.ID, c.Name)
				if c.Note != "" {
					fmt.Printf("  — %s", c.Note)
				}
				fmt.Println()
				for _, h := range c.Handles {
					star := ""
					if h.IsPrimary {
						star = " *"
					}
					fmt.Printf("      %s: %s%s\n", h.Platform, h.Handle, star)
				}
			}
			return nil
		},
	}

	var addNote string
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
			c, err := s.Create(contacts.Owner, client.Contact{Name: strings.Join(args, " "), Note: addNote})
			if err != nil {
				return err
			}
			fmt.Printf("Added #%d %s ✅\n", c.ID, c.Name)
			return nil
		},
	}
	addCmd.Flags().StringVar(&addNote, "note", "", "An optional note about the contact")

	var editNote string
	editCmd := &cobra.Command{
		Use:   "edit <id> <name>",
		Short: "Rename a contact / change its note",
		Args:  cobra.MinimumNArgs(2),
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
			if err := s.Update(contacts.Owner, id, strings.Join(args[1:], " "), editNote); err != nil {
				return err
			}
			fmt.Println("Updated ✅")
			return nil
		},
	}
	editCmd.Flags().StringVar(&editNote, "note", "", "Replace the contact's note")

	rmCmd := &cobra.Command{
		Use:   "rm <id>",
		Short: "Remove a contact (and its handles)",
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

	handleCmd := &cobra.Command{Use: "handle", Short: "Manage a contact's platform handles"}
	var primary bool
	handleAdd := &cobra.Command{
		Use:   "add <contact-id> <platform> <handle>",
		Short: "Attach a platform handle (telegram|whatsapp|discord|viber)",
		Args:  cobra.ExactArgs(3),
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
			if _, err := s.AddHandle(contacts.Owner, id, client.ContactHandle{
				Platform: args[1], Handle: args[2], IsPrimary: primary,
			}); err != nil {
				return err
			}
			fmt.Println("Handle added ✅")
			return nil
		},
	}
	handleAdd.Flags().BoolVar(&primary, "primary", false, "Mark this handle as the contact's primary for its platform")
	handleRm := &cobra.Command{
		Use:   "rm <platform> <handle>",
		Short: "Remove a platform handle",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := openContacts()
			if err != nil {
				return err
			}
			defer s.Close()
			if err := s.RemoveHandle(contacts.Owner, args[0], args[1]); err != nil {
				return err
			}
			fmt.Println("Handle removed ✅")
			return nil
		},
	}
	handleCmd.AddCommand(handleAdd, handleRm)

	contactsCmd.AddCommand(listCmd, addCmd, editCmd, rmCmd, handleCmd)
	rootCmd.AddCommand(contactsCmd)
}
