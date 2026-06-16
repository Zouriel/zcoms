package cmd

import (
	"zcoms/internal/config"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "zc",
	Short: "zcoms — Telegram + WhatsApp comms from your terminal",
	Long: "zcoms (zc) sends & receives messages and media across Telegram (`zc tg`)\n" +
		"and WhatsApp (`zc wa`), and ships an agent bridge that drives Claude/Codex\n" +
		"from your chats. The cross-channel agent commands live at the root.",
}

var AppConfig config.Config
var ConfigFilePath string

func Execute() error {
	loadedConfig, loadedConfigPath, err := config.LoadOrCreate()
	if err != nil {
		return err
	}

	AppConfig = loadedConfig
	ConfigFilePath = loadedConfigPath

	return rootCmd.Execute()
}
