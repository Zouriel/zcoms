package cmd

import (
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/internal/agent"

	"github.com/spf13/cobra"
)

func init() {
	triageCommand := &cobra.Command{
		Use:   "triage [schedule|on|off]",
		Short: "Show or set the message-triage schedule",
		Long: "Schedules: 30m, 1h, 2h, 3h, 6h, 12h, twice-daily (morning + ~10pm).\n" +
			"Also accepts `on` / `off`. Changes apply without restarting the daemon.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, path, err := agent.LoadOrSeedSettings()
			if err != nil {
				return err
			}

			if len(args) == 0 {
				state := "off"
				if s.Triage.Enabled {
					state = "on"
				}
				fmt.Printf("Triage:   %s\n", state)
				fmt.Printf("Schedule: %s\n", s.Triage.Describe())
				fmt.Printf("Dir:      %s\n", s.Triage.Dir)
				fmt.Printf("Config:   %s\n", path)
				fmt.Println("Set with: zc triage <" + strings.Join(agent.TriageSchedules, "|") + "|on|off>")
				return nil
			}

			arg := strings.ToLower(strings.TrimSpace(args[0]))
			switch arg {
			case "on":
				s.Triage.Enabled = true
			case "off":
				s.Triage.Enabled = false
			default:
				if !agent.ValidTriageSchedule(arg) {
					return fmt.Errorf("invalid schedule %q; use one of: %s (or on/off)", arg, strings.Join(agent.TriageSchedules, ", "))
				}
				s.Triage.Schedule = arg
				s.Triage.Enabled = true
			}

			if _, err := agent.SaveSettings(s); err != nil {
				return err
			}

			state := "off"
			if s.Triage.Enabled {
				state = "on"
			}
			fmt.Printf("Triage %s — %s\n", state, s.Triage.Describe())
			fmt.Println("(applies within a cycle; no restart needed)")
			return nil
		},
	}

	rootCmd.AddCommand(triageCommand)
}
