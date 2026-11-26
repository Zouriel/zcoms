package cmd

import (
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/internal/config"

	"github.com/spf13/cobra"
)

func init() {
	configCommand := &cobra.Command{
		Use:   "config",
		Short: "Show or change comms settings (config.json)",
	}

	ownerCommand := &cobra.Command{
		Use:   "owner [contact]",
		Short: "Show or set the owner: the contact added to every group the daemon creates",
		Long: "The owner is a contact in your directory (zc contacts). Every group created\n" +
			"with `zc tg group create` or `zc wa group create` also gets the owner added,\n" +
			"resolved on that group's platform. With no argument, shows the current owner.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				if strings.TrimSpace(AppConfig.Owner) == "" {
					fmt.Println("No owner set. Set one with `zc config owner <contact>`.")
					return nil
				}
				fmt.Println(AppConfig.Owner)
				return nil
			}
			name := strings.TrimSpace(strings.Join(args, " "))
			s, err := openContacts()
			if err != nil {
				return err
			}
			defer s.Close()
			matches, err := s.Resolve(name)
			if err != nil {
				return err
			}
			var exact []string
			for _, c := range matches {
				for _, cand := range append([]string{c.Name}, c.Aliases...) {
					if strings.EqualFold(strings.TrimSpace(cand), name) {
						exact = append(exact, c.Name)
						break
					}
				}
			}
			switch {
			case len(exact) == 1:
				name = exact[0]
			case len(exact) > 1:
				return fmt.Errorf("%q matches several contacts (%s): say which one", name, strings.Join(exact, ", "))
			case len(matches) == 1:
				name = matches[0].Name
			case len(matches) == 0:
				return fmt.Errorf("no contact named %q: add them with `zc contacts add`", name)
			default:
				names := make([]string, 0, len(matches))
				for _, c := range matches {
					names = append(names, c.Name)
				}
				return fmt.Errorf("%q matches several contacts (%s): say which one", name, strings.Join(names, ", "))
			}
			AppConfig.Owner = name
			if err := config.Save(AppConfig, ConfigFilePath); err != nil {
				return err
			}
			fmt.Printf("Owner set to %s\n", name)
			return nil
		},
	}

	configCommand.AddCommand(ownerCommand)
	rootCmd.AddCommand(configCommand)
}
