package cmd

import (
	"fmt"
	"strings"

	"zcoms/internal/agent"

	"github.com/spf13/cobra"
)

func init() {
	agentsCommand := &cobra.Command{
		Use:   "agents",
		Short: "Show or set which agent (claude/codex) handles which task",
		Long: "Shows installed agents and the configured default + per-task overrides\n" +
			"(edit agents.json directly, or use `zc agents set <task> <agent>`).",
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

			fmt.Printf("Default (bridge work): %s\n", cfg.For("", ""))
			fmt.Println("Per-task:")
			fmt.Printf("  triage -> %s\n", cfg.For("triage", ""))
			for task := range cfg.Tasks {
				if task != "triage" {
					fmt.Printf("  %s -> %s\n", task, cfg.For(task, ""))
				}
			}
			fmt.Println("Config:", path)
			return nil
		},
	}

	setCommand := &cobra.Command{
		Use:   "set <task|default> <claude|codex>",
		Short: "Assign an agent to a task (task names: default, triage)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := strings.ToLower(strings.TrimSpace(args[0]))
			backend := agent.Backend(strings.ToLower(strings.TrimSpace(args[1])))

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
