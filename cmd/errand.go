package cmd

import (
	"fmt"
	"strings"

	"tg/internal/agent"

	"github.com/spf13/cobra"
)

// errandUnavailable is shown when no daemon is running — errands live inside the
// daemon (it owns the Telegram session and drives the conversation), so there's
// no standalone fallback.
const errandUnavailable = "the agent bridge isn't running — start it with `tg init agent` (errands run inside the daemon)"

func init() {
	errandCommand := &cobra.Command{
		Use:   "errand",
		Short: "Dispatch an agent to message a contact, ask questions, and produce a deliverable",
		Long: "An errand is a friendly, autonomous task: the agent messages a contact, asks for\n" +
			"what's needed ONE question at a time (telling them how many remain), collects their\n" +
			"answers and any files, builds the deliverable, then sends you the file(s) plus a\n" +
			"summary and pings you when done. Errands run inside the `tg init agent` daemon.",
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
			handled, reply, err := agent.DaemonErrandStart(target, brief, deliver, start)
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

	listCommand := &cobra.Command{
		Use:   "list",
		Short: "List active errands",
		RunE: func(cmd *cobra.Command, args []string) error {
			handled, reply, err := agent.DaemonErrandList()
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
			handled, reply, err := agent.DaemonErrandCancel(args[0])
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

	errandCommand.AddCommand(startCommand, listCommand, cancelCommand)
	rootCmd.AddCommand(errandCommand)
}
