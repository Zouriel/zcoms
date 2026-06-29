package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/internal/components"

	"github.com/spf13/cobra"
)

const daemonUnit = "zcoms-daemon.service"

func init() {
	installCommand := &cobra.Command{
		Use:   "install [agent|team|console|commerce]",
		Short: "Install a tier (agent AI layer, or a module like team/console/commerce)",
		Long: "zcoms ships with Telegram + WhatsApp comms (`zc tg` / `zc wa`). Everything\n" +
			"above is an opt-in tier — its own pure-Go binary, composing over IPC:\n\n" +
			"  agent    — the AI layer: bridge, triage, errands, session manager (agent.db)\n" +
			"  team     — team coordination, delegation, GitHub sync, standups (needs agent)\n" +
			"  console  — owner-only local web UI to edit every store (needs agent)\n" +
			"  commerce — Telegram-Stars commerce control plane (core-only; no agent)\n\n" +
			"Run with no argument to see what's installed. Installing a module pulls in\n" +
			"its dependencies (the agent, and the comms daemon) automatically. Each\n" +
			"install fetches the prebuilt binary and runs it as a systemd user service.",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return printComponentStatus()
			}
			force, _ := cmd.Flags().GetBool("force")
			return runInstall(strings.ToLower(strings.TrimSpace(args[0])), force)
		},
	}

	uninstallCommand := &cobra.Command{
		Use:          "uninstall <agent|team|console|commerce>",
		Short:        "Remove an installed component (and anything that depends on it)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(strings.ToLower(strings.TrimSpace(args[0])))
		},
	}

	installCommand.Flags().Bool("force", false, "Re-seed, re-download, and re-activate even if already installed")

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
// With force, an already-installed component is re-seeded, re-downloaded, and
// re-activated (used to migrate a pre-component install onto the real binary).
func runInstall(name string, force bool) error {
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
		isTarget := dep == meta.Name
		if state.IsInstalled(dep) && !(force && isTarget) {
			continue
		}
		if err := seedComponent(dep); err != nil {
			return fmt.Errorf("seeding %s: %w", dep, err)
		}
		state.Installed[dep] = true
		newlyInstalled = append(newlyInstalled, dep)
	}

	if len(newlyInstalled) == 0 {
		fmt.Printf("%s is already installed (use --force to re-activate).\n", meta.Name)
		return nil
	}

	if err := components.Save(state); err != nil {
		return err
	}

	// Activate each new component (deps first): bridge sets up the core daemon;
	// triage/errands fetch their prebuilt binary and run it as their own service.
	for _, c := range newlyInstalled {
		fmt.Printf("✅ installed %s\n", c)
		if err := activateComponent(c); err != nil {
			fmt.Printf("⚠️  %s: %v\n", c, err)
		}
	}

	printPostInstallHints(newlyInstalled)
	return nil
}

// installable → its GitHub repo + binary name, for the prebuilt-release download.
var componentArtifact = map[components.Name]struct{ repo, bin string }{
	components.Agent:    {"Zouriel/zcoms-agent", "zcoms-agent"},
	components.Team:     {"Zouriel/zcoms-team", "zcoms-team"},
	components.Console:  {"Zouriel/zcoms-console", "zcoms-console"},
	components.Commerce: {"Zouriel/zcoms-commerce", "zcoms-commerce"},
}

// componentEnvFile maps a component to an optional systemd EnvironmentFile in the
// config dir. Commerce reads its runtime URL + bearer token from there so the
// secret never lives in a repo or in any store the console can read. The unit is
// written with a leading '-' so a missing file is tolerated.
var componentEnvFile = map[components.Name]string{
	components.Commerce: "commerce.env",
}

// activateComponent makes a freshly-installed tier live: it fetches the prebuilt
// binary and runs it as its own systemd user service. The agent (the lowest
// installable tier) also ensures the core comms daemon — which owns the Telegram
// session and serves IPC — is installed and running first, since every tier
// dials it.
func activateComponent(c components.Name) error {
	if c == components.Agent {
		if err := ensureDaemonService(); err != nil {
			fmt.Printf("⚠️  could not set up the daemon service automatically: %v\n", err)
			fmt.Println("   Install it manually, then: systemctl --user enable --now " + daemonUnit)
		} else {
			restartDaemon()
		}
	}
	art, ok := componentArtifact[c]
	if !ok {
		return nil
	}
	fmt.Printf("   ↓ downloading %s…\n", art.bin)
	if err := fetchComponentBinary(art.repo, art.bin); err != nil {
		return fmt.Errorf("download failed: %w (build from source: github.com/%s)", err, art.repo)
	}
	return ensureComponentService(componentUnitName(c), fmt.Sprintf("zcoms %s component", c), art.bin, componentEnvFile[c])
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
		deactivateComponent(c)
	}
	fmt.Println("Its commands are now hidden from `zc --help`; reinstall with `zc install " + name + "`.")
	return nil
}

// deactivateComponent stops a removed tier's service. The comms daemon is core
// and is left running (other tiers or plain `zc tg` may still need it).
func deactivateComponent(c components.Name) {
	_ = runSystemctl("disable", "--now", componentUnitName(c))
}

// seedComponent is a no-op: each tier owns its own state (the agent seeds/migrates
// agent.db on first run; modules own their db). Comms no longer seeds anything.
func seedComponent(name components.Name) error {
	_ = name
	return nil
}

func printPostInstallHints(installed []components.Name) {
	for _, c := range installed {
		switch c {
		case components.Agent:
			fmt.Println("   • Allow yourself in:    zc agent allowlist add <@you> full")
			fmt.Println("   • Set discovery roots:  zc agent settings set discovery_roots <path[,path]>")
			fmt.Println("   • Find your repos:      zc agent workspace sync")
			fmt.Println("   • Pick a backend:       zc agent persona set bridge backend <claude|codex>")
		case components.Team:
			fmt.Println("   • Create a project:   zc team delegator create <name>")
			fmt.Println("   • Add staff:          zc team staff add <delegator> <@user> <role> <limit>")
			fmt.Println("   • Schedule a standup: zc team standup create <name> <delegator> <@group> <HH:MM> <tz>")
		case components.Console:
			fmt.Println("   • Open the console:   zc console   (then log in — localhost only)")
		case components.Commerce:
			fmt.Println("   • Point at the runtime: write ~/.config/zcoms/commerce.env with")
			fmt.Println("       ZC_COMMERCE_RUNTIME_URL=…  and  ZC_COMMERCE_RUNTIME_TOKEN=…")
			fmt.Println("   • Then restart:         systemctl --user restart zcoms-commerce.service")
			fmt.Println("   • Check the link:       zc commerce status")
			fmt.Println("   • Onboard a store:      zc commerce new store")
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

// --- component binaries (prebuilt release download) -------------------------

func componentUnitName(c components.Name) string { return "zcoms-" + string(c) + ".service" }

// platformAsset is the release-asset suffix for the host, e.g. "linux-x64".
func platformAsset() string {
	arch := runtime.GOARCH
	switch arch {
	case "amd64":
		arch = "x64"
	case "arm64":
		arch = "arm64"
	}
	return runtime.GOOS + "-" + arch
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// fetchComponentBinary downloads the prebuilt component binary for this platform
// from the repo's latest GitHub release into ~/.local/bin/<bin>.
func fetchComponentBinary(repo, bin string) error {
	// Generous timeout: it caps the whole exchange including the body download.
	client := &http.Client{Timeout: 5 * time.Minute}
	apiURL := "https://api.github.com/repos/" + repo + "/releases/latest"
	req, _ := http.NewRequest("GET", apiURL, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("github API %s: %s", apiURL, resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return err
	}
	want := bin + "-" + platformAsset()
	var dlURL string
	for _, a := range rel.Assets {
		if a.Name == want {
			dlURL = a.URL
			break
		}
	}
	if dlURL == "" {
		return fmt.Errorf("no prebuilt %q in %s release %s", want, repo, rel.TagName)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(binDir, bin)
	tmp := dest + ".download"

	dr, err := client.Get(dlURL)
	if err != nil {
		return err
	}
	defer dr.Body.Close()
	if dr.StatusCode != 200 {
		return fmt.Errorf("download %s: %s", dlURL, dr.Status)
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, dr.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	f.Close()
	if err := os.Rename(tmp, dest); err != nil { // atomic replace (works even if running)
		_ = os.Remove(tmp)
		return err
	}
	fmt.Printf("   installed %s (%s)\n", dest, rel.TagName)
	return nil
}

// ensureComponentService writes+enables a component's own user unit running its
// downloaded binary.
func ensureComponentService(unitName, desc, bin, envFile string) error {
	dir, err := userUnitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	exe := filepath.Join(home, ".local", "bin", bin)
	// Optional EnvironmentFile (e.g. commerce.env for the runtime token). The
	// leading '-' tells systemd to tolerate it being absent, so the unit still
	// starts before the file is written.
	envLine := ""
	if envFile != "" {
		envLine = "EnvironmentFile=-" + filepath.Join(home, ".config", "zcoms", envFile) + "\n"
	}
	unit := fmt.Sprintf(`[Unit]
Description=%s
After=network-online.target %s
Wants=%s

[Service]
Type=simple
%sExecStart=%s
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`, desc, daemonUnit, daemonUnit, envLine, exe)
	if err := os.WriteFile(filepath.Join(dir, unitName), []byte(unit), 0o644); err != nil {
		return err
	}
	_ = runSystemctl("daemon-reload")
	if err := runSystemctl("enable", unitName); err != nil {
		return fmt.Errorf("enable %s: %w", unitName, err)
	}
	// restart (not just enable --now) so a re-install picks up the new binary
	// even when the service is already running.
	if err := runSystemctl("restart", unitName); err != nil {
		return fmt.Errorf("restart %s: %w", unitName, err)
	}
	fmt.Println("🔄 (re)started", unitName)
	return nil
}
