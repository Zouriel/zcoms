package cmd

import (
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/client"

	"github.com/spf13/cobra"
)

// errandUnavailable is shown when the errands component isn't running.
const errandUnavailable = "the agent isn't running — install it with `zc install agent`"

// errandsCommand forwards an errand command line to the agent (errands fold into
// the agent tier). handled is false when the agent socket isn't listening.
func errandsCommand(cmdline string) (handled bool, reply string, err error) {
	res, err := client.ModuleCommand("agent.sock", cmdline, "")
	return res.Running, res.Reply, err
}

func init() {
	errandCommand := &cobra.Command{
		Use:   "errand",
		Short: "Dispatch an agent to message a contact, ask questions, and produce a deliverable",
		Long: "An errand is a friendly, autonomous task: the agent messages a contact, asks for\n" +
			"what's needed ONE question at a time (telling them how many remain), collects their\n" +
			"answers and any files, builds the deliverable, then sends you the file(s) plus a\n" +
			"summary and pings you when done. Errands run in the `zcoms-errands` component.",
	}

	var deliver, start bool
	startCommand := &cobra.Command{
		Use:   "start <@user|wa:NUMBER|#index> <brief...>",
		Short: "Start an errand at a contact",
		Long: "Dispatch an errand. The target is a Telegram @username/chat id, a WhatsApp\n" +
			"contact as wa:<number>, or #<index> from the last triage batch. By default the\n" +
			"agent drafts a plan and waits for your approval before messaging anyone; pass\n" +
			"--go to start immediately, and --deliver to also send the finished file to the\n" +
			"contact.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			brief := strings.Join(args[1:], " ")
			cmdline := "errand start "
			if deliver {
				cmdline += "deliver "
			}
			if start {
				cmdline += "go "
			}
			cmdline += target + " | " + brief
			handled, reply, err := errandsCommand(cmdline)
			if !handled {
				return fmt.Errorf(errandUnavailable)
			}
			if err != nil {
				return err
			}
			fmt.Println(reply)
			return nil
		},
	}
	startCommand.Flags().BoolVar(&deliver, "deliver", false, "Also send the finished deliverable to the contact")
	startCommand.Flags().BoolVar(&start, "go", false, "Skip the approval step and start messaging immediately")

	var schedDeliver, schedGo bool
	scheduleCommand := &cobra.Command{
		Use:   "schedule <@user|wa:NUMBER|#index> <when> <brief...>",
		Short: "Schedule an errand to start at a future time",
		Long: "Queue an errand to be dispatched automatically at a specific time. <when> is\n" +
			"a relative duration (+30m, +2h, 1h30m), a wall-clock time today/tomorrow\n" +
			"(15:30), or a full local timestamp (2026-06-18T15:30). When it fires it behaves\n" +
			"exactly like `errand start`: it drafts a plan for your approval by default, or\n" +
			"starts messaging immediately with --go. The target is resolved at fire time.",
		Args: cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			when := args[1]
			brief := strings.Join(args[2:], " ")
			cmdline := "errand schedule "
			if schedDeliver {
				cmdline += "deliver "
			}
			if schedGo {
				cmdline += "go "
			}
			cmdline += target + " at " + when + " | " + brief
			handled, reply, err := errandsCommand(cmdline)
			if !handled {
				return fmt.Errorf(errandUnavailable)
			}
			if err != nil {
				return err
			}
			fmt.Println(reply)
			return nil
		},
	}
	scheduleCommand.Flags().BoolVar(&schedDeliver, "deliver", false, "Also send the finished deliverable to the contact")
	scheduleCommand.Flags().BoolVar(&schedGo, "go", false, "Skip the approval step and start messaging immediately when it fires")

	scheduledCommand := &cobra.Command{
		Use:   "scheduled",
		Short: "List errands queued to fire at a future time",
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, reply, err := errandsCommand("errand scheduled")
			if !handled {
				return fmt.Errorf(errandUnavailable)
			}
			if err != nil {
				return err
			}
			fmt.Println(reply)
			return nil
		},
	}

	unscheduleCommand := &cobra.Command{
		Use:   "unschedule <id>",
		Short: "Cancel a scheduled errand before it fires",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, reply, err := errandsCommand("errand unschedule " + args[0])
			if !handled {
				return fmt.Errorf(errandUnavailable)
			}
			if err != nil {
				return err
			}
			fmt.Println(reply)
			return nil
		},
	}

	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List active errands",
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, reply, err := errandsCommand("errand list")
			if !handled {
				return fmt.Errorf(errandUnavailable)
			}
			if err != nil {
				return err
			}
			fmt.Println(reply)
			return nil
		},
	}

	cancelCommand := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel an errand (the contact stops being messaged)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, reply, err := errandsCommand("errand cancel " + args[0])
			if !handled {
				return fmt.Errorf(errandUnavailable)
			}
			if err != nil {
				return err
			}
			fmt.Println(reply)
			return nil
		},
	}

	errandCommand.AddCommand(startCommand, scheduleCommand, scheduledCommand, unscheduleCommand, listCommand, cancelCommand)
	rootCmd.AddCommand(errandCommand)
}
