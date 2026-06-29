package cmd

import (
	"fmt"

	"github.com/Zouriel/zcoms/internal/components"
	"github.com/Zouriel/zcoms/internal/config"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "zc",
	Short: "zcoms — Telegram + WhatsApp comms from your terminal",
	Long: "zcoms (zc) sends & receives messages and media across Telegram (`zc tg`)\n" +
		"and WhatsApp (`zc wa`), and manages a contacts directory (`zc contacts`). The\n" +
		"AI layer (bridge, triage, errands, session manager) and modules (team, console,\n" +
		"commerce) are opt-in tiers — add them with `zc install <agent|team|console|commerce>`.",
}

var AppConfig config.Config
var ConfigFilePath string

// gatedCommands maps a root command name to a component that must be installed
// for it to be usable. In the 3-repo architecture the agent-driving commands
// (init/allowlist/locations/agents/triage/errand/agent/team) are thin clients of
// the agent/module sockets and self-report when their tier isn't running
// ("install it with `zc install agent`"), so nothing is hard-gated here. Left as
// a seam for any future module that genuinely needs hiding until installed.
var gatedCommands = map[string]components.Name{}

func Execute() error {
	loadedConfig, loadedConfigPath, err := config.LoadOrCreate()
	if err != nil {
		return err
	}

	AppConfig = loadedConfig
	ConfigFilePath = loadedConfigPath

	applyComponentGating()

	return rootCmd.Execute()
}

// applyComponentGating hides commands whose component isn't installed and makes
// invoking them (by name) fail with an install hint instead of running.
func applyComponentGating() {
	state, err := components.Load()
	if err != nil {
		return // fail open: don't block the CLI if the registry can't be read
	}
	for _, c := range rootCmd.Commands() {
		comp, ok := gatedCommands[c.Name()]
		if !ok || state.IsInstalled(comp) {
			continue
		}
		c.Hidden = true
		c.SilenceUsage = true
		c.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("the %q component isn't installed — run: zc install %s", comp, comp)
		}
	}
}
