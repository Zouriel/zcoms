package cmd

import (
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/internal/agent"

	"github.com/spf13/cobra"
)

func init() {
	locationsCommand := &cobra.Command{
		Use:   "locations",
		Short: "List the agent-bridge project locations",
		RunE: func(cmd *cobra.Command, args []string) error {
			locs, path, err := agent.LoadOrSeedLocations()
			if err != nil {
				return err
			}
			names := locs.SortedNames()
			if len(names) == 0 {
				fmt.Println("No locations configured.")
			}
			for _, name := range names {
				cfg := locs[name]
				cap := ""
				if cfg.MaxRole != "" {
					cap = fmt.Sprintf("  [max: %s]", cfg.MaxRole)
				}
				fmt.Printf("  %-16s %s%s\n", name, cfg.Path, cap)
			}
			fmt.Println("Config:", path)
			return nil
		},
	}

	addCommand := &cobra.Command{
		Use:   "add <name> <path> [max_role]",
		Short: "Add or update a location (max_role: read|confirm|edit|full)",
		Args:  cobra.RangeArgs(2, 3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			path := expandUserPath(strings.TrimSpace(args[1]))

			cfg := agent.LocationConfig{Path: path}
			if len(args) == 3 {
				role := agent.Role(strings.ToLower(strings.TrimSpace(args[2])))
				if !agent.ValidRole(role) {
					return fmt.Errorf("max_role must be one of: read, confirm, edit, full")
				}
				cfg.MaxRole = role
			}

			locs, _, err := agent.LoadOrSeedLocations()
			if err != nil {
				return err
			}
			locs[name] = cfg
			p, err := agent.SaveLocations(locs)
			if err != nil {
				return err
			}
			fmt.Printf("Added %q -> %s%s (%s)\n", name, cfg.Path, capSuffix(cfg.MaxRole), p)
			return nil
		},
	}

	removeCommand := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a location",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			locs, _, err := agent.LoadOrSeedLocations()
			if err != nil {
				return err
			}
			if _, ok := locs[name]; !ok {
				return fmt.Errorf("no location named %q", name)
			}
			delete(locs, name)
			p, err := agent.SaveLocations(locs)
			if err != nil {
				return err
			}
			fmt.Printf("Removed %q (%s)\n", name, p)
			return nil
		},
	}

	locationsCommand.AddCommand(addCommand, removeCommand)
	rootCmd.AddCommand(locationsCommand)
}

func capSuffix(role agent.Role) string {
	if role == "" {
		return ""
	}
	return "  [max: " + string(role) + "]"
}
