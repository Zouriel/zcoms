package cmd

import (
	"fmt"

	"zcoms/internal/components"
	"zcoms/internal/config"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "zc",
	Short: "zcoms — Telegram + WhatsApp comms from your terminal",
	Long: "zcoms (zc) sends & receives messages and media across Telegram (`zc tg`)\n" +
		"and WhatsApp (`zc wa`). The agent features — bridge, triage, errands — are\n" +
		"opt-in components; add them with `zc install <component>` (see `zc install`).",
}

var AppConfig config.Config
var ConfigFilePath string

// gatedCommands maps a root command name to the component that must be installed
// for it to be usable. Until then the command is hidden from `zc --help` and
// running it prints an install hint. Core comms (`tg`/`wa`) and `install`/
// `uninstall` are never gated.
var gatedCommands = map[string]components.Name{
	"init":      components.Bridge,
	"allowlist": components.Bridge,
	"locations": components.Bridge,
	"agents":    components.Bridge,
	"triage":    components.Triage,
	"errand":    components.Errands,
}

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
