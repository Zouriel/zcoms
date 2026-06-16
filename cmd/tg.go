package cmd

import "github.com/spf13/cobra"

// tgCmd is the parent for the Telegram communication commands (login, send,
// chats, tail, …). It mirrors `zc wa` so Telegram and WhatsApp read the same:
// `zc tg send …` / `zc wa send …`. The cross-channel agent commands (init,
// allowlist, agents, locations, triage, errand) stay at the root.
var tgCmd = &cobra.Command{
	Use:   "tg",
	Short: "Telegram (login, send, ask, tail chats, download media)",
	Long: "Telegram communication commands built on TDLib. When the agent bridge\n" +
		"(`zc init agent`) is running it owns the single Telegram session and these\n" +
		"route through it; otherwise they open their own short-lived session.",
}

func init() {
	rootCmd.AddCommand(tgCmd)
}
