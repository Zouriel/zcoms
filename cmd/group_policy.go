package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// shellQuote quotes s for the agent's quote-aware splitter when it holds
// whitespace or quotes: double quotes, with \ and " escaped.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\") {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}

func quoteArgs(args []string) string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		out = append(out, shellQuote(a))
	}
	return strings.Join(out, " ")
}

func init() {
	var wa bool
	groupCommand := &cobra.Command{
		Use:   "group on|off <group>",
		Short: "Turn the agent on or off in a group (a general group: answers calls, web only)",
		Long: "Turns the agent on (or off) in a group. When on, anyone in the group can call\n" +
			"it with an @mention or by replying to one of its messages; it answers that one\n" +
			"message, briefly, with web search only. Every other message is ignored.\n" +
			"Groups are off until turned on. Use --wa for a WhatsApp group (default Telegram).\n" +
			"For a group bound to a repo, use `zc project init` instead.",
		Args:         cobra.MinimumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			state := strings.ToLower(args[0])
			if state != "on" && state != "off" {
				return fmt.Errorf("say on or off, then the group")
			}
			transportName := "telegram"
			if wa {
				transportName = "whatsapp"
			}
			group := strings.Join(args[1:], " ")
			return forwardToAgent("group " + state + " " + transportName + " " + shellQuote(group))
		},
	}
	groupCommand.Flags().BoolVar(&wa, "wa", false, "The group is on WhatsApp")
	rootCmd.AddCommand(groupCommand)
}
