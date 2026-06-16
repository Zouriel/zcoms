package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"zcoms/internal/agent"
	"zcoms/internal/components"

	"github.com/spf13/cobra"
)

const daemonUnit = "zcoms-daemon.service"

func init() {
	installCommand := &cobra.Command{
		Use:   "install [bridge|triage|errands]",
		Short: "Install an optional component (agent bridge, triage, errands)",
		Long: "zcoms ships with Telegram + WhatsApp comms (`zc tg` / `zc wa`). The agent\n" +
			"features are opt-in components that run inside the bridge daemon:\n\n" +
			"  bridge   — interactive agent sessions: locations, session management, chat\n" +
			"  triage   — scheduled AI digest of incoming messages (configure with `zc triage`)\n" +
			"  errands  — dispatch autonomous interviewer→producer agents (needs bridge)\n\n" +
			"Run with no argument to see what's installed. Installing triage or errands\n" +
			"pulls in bridge automatically. Each install (re)starts the daemon so the\n" +
			"component is active immediately, and its commands appear in `zc --help`.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return printComponentStatus()
			}
			return runInstall(strings.ToLower(strings.TrimSpace(args[0])))
		},
	}

	uninstallCommand := &cobra.Command{
		Use:          "uninstall <bridge|triage|errands>",
		Short:        "Remove an installed component (and anything that depends on it)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(strings.ToLower(strings.TrimSpace(args[0])))
		},
	}

	rootCmd.AddCommand(installCommand, uninstallCommand)
}

// printComponentStatus lists every component and whether it's installed.
func printComponentStatus() error {
	state, err := components.Load()
	if err != nil {
		return err
	}
	fmt.Println("Components (core `zc tg` / `zc wa` are always available):")
	for _, m := range components.All() {
		mark := "—  not installed"
		if state.IsInstalled(m.Name) {
			mark = "✓  installed"
		}
		req := ""
		if len(m.Requires) > 0 {
			names := make([]string, len(m.Requires))
			for i, r := range m.Requires {
				names[i] = string(r)
			}
			req = "  (needs " + strings.Join(names, ", ") + ")"
		}
		fmt.Printf("  %-8s %-18s %s%s\n", m.Name, mark, m.Summary, req)
	}
	fmt.Println("\nInstall with:   zc install <component>")
	fmt.Println("Remove with:    zc uninstall <component>")
	return nil
}

// runInstall installs a component and its dependency closure, then activates it.
func runInstall(name string) error {
	meta, ok := components.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown component %q — choose one of: %s", name, componentNames())
	}

	state, err := components.Load()
	if err != nil {
		return err
	}

	// Install dependencies first, then the component itself.
	var newlyInstalled []components.Name
	for _, dep := range append(components.Requires(meta.Name), meta.Name) {
		if state.IsInstalled(dep) {
			continue
		}
		if err := seedComponent(dep); err != nil {
			return fmt.Errorf("seeding %s: %w", dep, err)
		}
		state.Installed[dep] = true
		newlyInstalled = append(newlyInstalled, dep)
	}

	if len(newlyInstalled) == 0 {
		fmt.Printf("%s is already installed.\n", meta.Name)
		return nil
	}

	if err := components.Save(state); err != nil {
		return err
	}

	for _, c := range newlyInstalled {
		fmt.Printf("✅ installed %s\n", c)
	}

	// Activate: ensure the daemon service exists/enabled, then restart so the
	// daemon picks up the new component(s).
	if err := ensureDaemonService(); err != nil {
		fmt.Printf("⚠️  could not set up the daemon service automatically: %v\n", err)
		fmt.Println("   Install it manually, then: systemctl --user enable --now " + daemonUnit)
	}
	restartDaemon()

	printPostInstallHints(newlyInstalled)
	return nil
}

// runUninstall removes a component and any components that depend on it.
func runUninstall(name string) error {
	meta, ok := components.Lookup(name)
	if !ok {
		return fmt.Errorf("unknown component %q — choose one of: %s", name, componentNames())
	}
	state, err := components.Load()
	if err != nil {
		return err
	}

	var removed []components.Name
	for _, dep := range append(components.Dependents(meta.Name), meta.Name) {
		if !state.IsInstalled(dep) {
			continue
		}
		state.Installed[dep] = false
		if dep == components.Triage {
			disableTriage()
		}
		removed = append(removed, dep)
	}
	if len(removed) == 0 {
		fmt.Printf("%s isn't installed.\n", meta.Name)
		return nil
	}
	if err := components.Save(state); err != nil {
		return err
	}
	for _, c := range removed {
		fmt.Printf("🗑️  removed %s\n", c)
	}
	// Re-read state in the running daemon (it stops the gated loops).
	restartDaemon()
	fmt.Println("Its commands are now hidden from `zc --help`; reinstall with `zc install " + name + "`.")
	return nil
}

// seedComponent makes sure a component's config files exist (and flips on the
// features it owns) so it works the moment the daemon restarts.
func seedComponent(name components.Name) error {
	switch name {
	case components.Bridge:
		if _, _, err := agent.LoadOrSeedAllowlist(); err != nil {
			return err
		}
		if _, _, err := agent.LoadOrSeedLocations(); err != nil {
			return err
		}
		if _, _, err := agent.LoadOrSeedAgents(); err != nil {
			return err
		}
		if _, _, err := agent.LoadOrSeedSettings(); err != nil {
			return err
		}
	case components.Triage:
		s, _, err := agent.LoadOrSeedSettings()
		if err != nil {
			return err
		}
		s.Triage.Enabled = true
		if s.Triage.Schedule == "" && s.Triage.EveryMinutes == 0 {
			s.Triage.Schedule = "1h"
		}
		if _, err := agent.SaveSettings(s); err != nil {
			return err
		}
	case components.Errands:
		if _, _, err := agent.LoadOrSeedAgents(); err != nil {
			return err
		}
	}
	return nil
}

// disableTriage turns the triage schedule off when triage is uninstalled.
func disableTriage() {
	if s, _, err := agent.LoadOrSeedSettings(); err == nil {
		s.Triage.Enabled = false
		_, _ = agent.SaveSettings(s)
	}
}

func printPostInstallHints(installed []components.Name) {
	for _, c := range installed {
		switch c {
		case components.Bridge:
			fmt.Println("   • Allow yourself in:  zc allowlist add <@you> full")
			fmt.Println("   • Add a project:      zc locations add <name> <path>")
			fmt.Println("   • Pick its agent:     zc agents set bridge <claude|codex>")
		case components.Triage:
			fmt.Println("   • Configure schedule: zc triage <30m|1h|…|twice-daily>")
			fmt.Println("   • Pick its agent:     zc agents set triage <claude|codex>")
		case components.Errands:
			fmt.Println("   • Dispatch one:       zc errand start <@user|wa:NUMBER> <brief>")
			fmt.Println("   • Pick its agent:     zc agents set errands <claude|codex>")
		}
	}
}

func componentNames() string {
	names := make([]string, 0, len(components.All()))
	for _, m := range components.All() {
		names = append(names, string(m.Name))
	}
	return strings.Join(names, ", ")
}

// --- systemd service management ----------------------------------------------

func userUnitDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

// ensureDaemonService writes and enables the zcoms-daemon user unit if it isn't
// already present. An existing unit is left untouched (it may carry custom paths).
func ensureDaemonService() error {
	dir, err := userUnitDir()
	if err != nil {
		return err
	}
	unitPath := filepath.Join(dir, daemonUnit)
	if _, err := os.Stat(unitPath); err == nil {
		// Already installed — just make sure it's enabled.
		_ = runSystemctl("enable", daemonUnit)
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if abs, err := filepath.Abs(exe); err == nil {
		exe = abs
	}
	unit := fmt.Sprintf(`[Unit]
Description=zcoms agent bridge (zc init agent)
After=network-online.target
Wants=network-online.target
After=wa-bridge.service
Wants=wa-bridge.service

[Service]
Type=simple
WorkingDirectory=%s
ExecStart=%s init agent
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`, daemonWorkingDir(), exe)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	_ = runSystemctl("daemon-reload")
	if err := runSystemctl("enable", daemonUnit); err != nil {
		return err
	}
	fmt.Println("   installed systemd unit:", unitPath)
	return nil
}

// daemonWorkingDir picks a working directory for the daemon that contains a
// .env (for TG_API_ID/TG_API_HASH), falling back sensibly.
func daemonWorkingDir() string {
	candidates := []string{}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "personal", "zcoms"), home)
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, ".env")); err == nil {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return "%h"
}

// restartDaemon restarts the daemon if its unit is installed, so a freshly
// installed/removed component takes effect immediately.
func restartDaemon() {
	dir, err := userUnitDir()
	if err != nil {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, daemonUnit)); err != nil {
		fmt.Println("ℹ️  start the daemon to activate:  zc init agent  (or install it as a service)")
		return
	}
	if err := runSystemctl("restart", daemonUnit); err != nil {
		fmt.Printf("⚠️  restart the daemon to apply: systemctl --user restart %s (%v)\n", daemonUnit, err)
		return
	}
	fmt.Println("🔄 restarted", daemonUnit)
}

func runSystemctl(args ...string) error {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return fmt.Errorf("systemctl not found")
	}
	c := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	c.Stderr = os.Stderr
	return c.Run()
}
