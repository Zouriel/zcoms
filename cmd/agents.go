package cmd

import (
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/internal/agent"

	"github.com/spf13/cobra"
)

func init() {
	agentsCommand := &cobra.Command{
		Use:   "agents",
		Short: "Show or set which agent (claude/codex) handles which session type",
		Long: "Shows installed agents and the configured default + the agent per session\n" +
			"type — bridge, chat, triage, errands (edit agents.json, or use `zc agents set`).",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := agent.LoadOrSeedAgents()
			if err != nil {
				return err
			}

			available := agent.AvailableAgents()
			fmt.Print("Installed agents: ")
			if len(available) == 0 {
				fmt.Println("none — agent mode unavailable (install `claude` or `codex`)")
			} else {
				parts := make([]string, len(available))
				for i, b := range available {
					parts[i] = string(b)
				}
				fmt.Println(strings.Join(parts, ", "))
			}

			fmt.Printf("Default:  %s\n", cfg.For("", ""))
			fmt.Println("Per session type:")
			for _, t := range agent.SessionTypes {
				fmt.Printf("  %-8s -> %s\n", t, cfg.For(t, ""))
			}
			fmt.Println("Config:", path)
			return nil
		},
	}

	setCommand := &cobra.Command{
		Use:   "set <bridge|chat|triage|errands|default> <claude|codex>",
		Short: "Assign an agent to a session type (bridge, chat, triage, errands) or the default",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := strings.ToLower(strings.TrimSpace(args[0]))
			backend := agent.Backend(strings.ToLower(strings.TrimSpace(args[1])))

			if task != "default" && task != "" && !agent.IsSessionType(task) {
				return fmt.Errorf("unknown session type %q; use one of: %s (or default)", task, strings.Join(agent.SessionTypes, ", "))
			}
			if backend != agent.BackendClaude && backend != agent.BackendCodex {
				return fmt.Errorf("agent must be 'claude' or 'codex'")
			}
			if !agent.AgentAvailable(backend) {
				fmt.Printf("warning: `%s` isn't installed; the daemon will fall back to an available agent.\n", backend)
			}

			cfg, _, err := agent.LoadOrSeedAgents()
			if err != nil {
				return err
			}
			if task == "default" || task == "" {
				cfg.Default = backend
			} else {
				if cfg.Tasks == nil {
					cfg.Tasks = map[string]agent.Backend{}
				}
				cfg.Tasks[task] = backend
			}

			path, err := agent.SaveAgents(cfg)
			if err != nil {
				return err
			}
			fmt.Printf("Set %s -> %s (%s)\n", task, backend, path)
			fmt.Println("Restart the daemon to apply: systemctl --user restart zcoms-daemon")
			return nil
		},
	}

	agentsCommand.AddCommand(setCommand)
	rootCmd.AddCommand(agentsCommand)
}
