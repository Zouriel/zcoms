package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Zouriel/zcoms/internal/agent"

	"github.com/spf13/cobra"
)

func init() {
	allowlistCommand := &cobra.Command{
		Use:   "allowlist",
		Short: "List who may drive the agent bridge",
		RunE: func(cmd *cobra.Command, args []string) error {
			al, path, err := agent.LoadOrSeedAllowlist()
			if err != nil {
				return err
			}
			if len(al) == 0 {
				fmt.Println("Allowlist is empty.")
			}
			names := make([]string, 0, len(al))
			for u := range al {
				names = append(names, u)
			}
			sort.Strings(names)
			for _, u := range names {
				e := al[u]
				agentStr := "(default)"
				if e.Agent != "" {
					agentStr = string(e.Agent)
				}
				fmt.Printf("  %-18s role=%-7s agent=%-9s locations=%s\n",
					u, e.Role, agentStr, strings.Join(e.Locations, ","))
			}
			fmt.Println("Config:", path)
			return nil
		},
	}

	var agentFlag string
	addCommand := &cobra.Command{
		Use:   "add <@username> <role> [location...]",
		Short: "Add or update an allow-listed user (role: read|confirm|edit|full)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := normalizeUsername(args[0])
			role := agent.Role(strings.ToLower(strings.TrimSpace(args[1])))
			if !agent.ValidRole(role) {
				return fmt.Errorf("role must be one of: read, confirm, edit, full")
			}

			locations := args[2:]
			if len(locations) == 0 {
				locations = []string{"*"}
			}

			entry := agent.AllowEntry{Role: role, Locations: locations}
			if agentFlag != "" {
				backend := agent.Backend(strings.ToLower(strings.TrimSpace(agentFlag)))
				if backend != agent.BackendClaude && backend != agent.BackendCodex {
					return fmt.Errorf("--agent must be 'claude' or 'codex'")
				}
				entry.Agent = backend
			}

			// Warn about unknown location names (not fatal).
			if locs, _, err := agent.LoadOrSeedLocations(); err == nil {
				for _, name := range locations {
					if name == "*" {
						continue
					}
					if _, ok := locs[name]; !ok {
						fmt.Printf("warning: location %q isn't defined (zc locations add ...)\n", name)
					}
				}
			}

			al, _, err := agent.LoadOrSeedAllowlist()
			if err != nil {
				return err
			}
			al[username] = entry
			path, err := agent.SaveAllowlist(al)
			if err != nil {
				return err
			}

			fmt.Printf("Added %s (role=%s, locations=%s) -> %s\n", username, role, strings.Join(locations, ","), path)
			fmt.Printf("⚠️  This grants %s agent access to this machine (roles gate writes, not reads).\n", username)
			fmt.Println("Restart the daemon to apply: systemctl --user restart zcoms-daemon")
			return nil
		},
	}
	addCommand.Flags().StringVar(&agentFlag, "agent", "", "agent backend override: claude|codex")

	removeCommand := &cobra.Command{
		Use:     "remove <@username>",
		Aliases: []string{"rm"},
		Short:   "Remove an allow-listed user",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := normalizeUsername(args[0])
			al, _, err := agent.LoadOrSeedAllowlist()
			if err != nil {
				return err
			}
			if _, ok := al[username]; !ok {
				return fmt.Errorf("%s is not on the allowlist", username)
			}
			delete(al, username)
			path, err := agent.SaveAllowlist(al)
			if err != nil {
				return err
			}
			fmt.Printf("Removed %s (%s)\n", username, path)
			fmt.Println("Restart the daemon to apply: systemctl --user restart zcoms-daemon")
			return nil
		},
	}

	allowlistCommand.AddCommand(addCommand, removeCommand)
	rootCmd.AddCommand(allowlistCommand)
}

func normalizeUsername(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "@") {
		s = "@" + s
	}
	return s
}
