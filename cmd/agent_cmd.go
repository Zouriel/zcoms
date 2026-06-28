package cmd

import (
	"fmt"
	"strings"

	"github.com/Zouriel/zcoms/client"

	"github.com/spf13/cobra"
)

// ownerActor returns the owner's @handle for tagging agent/module commands.
func ownerActor() string {
	actor := strings.TrimSpace(AppConfig.Username)
	if actor != "" && !strings.HasPrefix(actor, "@") {
		actor = "@" + actor
	}
	return actor
}

// forwardToAgent sends a command line to the agent tier over agent.sock and
// prints its reply. The agent owns all AI config (personas, workspaces,
// sessions, allowlist, settings) in agent.db, so these `zc` verbs are thin
// pass-throughs — install the agent with `zc install agent`.
func forwardToAgent(line string) error {
	res, err := client.ModuleCommand("agent.sock", strings.TrimSpace(line), ownerActor())
	if !res.Running {
		return fmt.Errorf("the agent isn't running — install it with `zc install agent`")
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.Reply) != "" {
		fmt.Println(res.Reply)
	}
	return nil
}

// passthroughCmd builds a cobra command that forwards its whole line (prefixed
// with verb) to the agent socket.
func passthroughCmd(verb, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:                verb + " [command…]",
		Short:              short,
		Long:               long,
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return forwardToAgent(strings.TrimSpace(verb + " " + strings.Join(args, " ")))
		},
	}
}

func init() {
	// The primary interface to the agent tier: `zc agent workspace|session|
	// persona|allowlist|triage|errand …`. Everything after `agent` is parsed by
	// the agent process (it owns agent.db).
	agentCmd := passthroughCmd("agent",
		"Drive the agent tier: workspaces, sessions, personas, allowlist, settings",
		"Thin client of the zcoms-agent process (agent.sock). The agent owns the AI\n"+
			"state in agent.db — personas/seed prompts, the workspace registry + session\n"+
			"manager, the allowlist, and settings. Examples:\n"+
			"  zc agent workspace list|sync|cap|pin|ignore\n"+
			"  zc agent session start|resume|list|label <workspace>\n"+
			"  zc agent persona list|edit\n"+
			"  zc agent allowlist add|rm|list")
	rootCmd.AddCommand(agentCmd)
}
