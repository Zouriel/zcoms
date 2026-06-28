// Package components tracks which optional capabilities of zcoms are installed.
//
// The core binary always provides Telegram + WhatsApp comms (`zc tg` / `zc wa`).
// Everything that runs inside the long-running agent daemon — the interactive
// bridge, scheduled triage, and errands — is an opt-in component the user adds
// with `zc install <component>`. Because only one process can hold the Telegram
// (TDLib) session, these are capabilities of the single daemon, gated by this
// registry, rather than separate processes.
package components

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Zouriel/zcoms/internal/config"
)

// Name identifies an installable component.
type Name string

// In the 3-repo architecture the installables are tiers, each its own user-space
// binary over IPC: the agent (the whole AI layer — bridge, triage, errands,
// session manager) and the modules above it (team, console). Comms is the always-
// present core (`zc tg`/`zc wa`), never an installable.
const (
	Agent   Name = "agent"
	Team    Name = "team"
	Console Name = "console"
)

// Meta describes an installable: what it does and which tiers it requires.
type Meta struct {
	Name     Name
	Summary  string
	Requires []Name
}

// registry is the canonical list of installables, in install/display order.
// Each declares its dependency tier(s); `zc install` resolves the chain.
var registry = []Meta{
	{Agent, "AI layer — interactive bridge, triage, errands, session manager (agent.db)", nil},
	{Team, "Team coordination, task delegation, GitHub Projects sync, and standups", []Name{Agent}},
	{Console, "Owner-only local web UI to edit every store (contacts/workspaces/personas/…)", []Name{Agent}},
}

// All returns the component catalog in display order.
func All() []Meta { return registry }

// Lookup resolves a component name (case-sensitive, lowercase), reporting
// whether it exists.
func Lookup(name string) (Meta, bool) {
	for _, m := range registry {
		if string(m.Name) == name {
			return m, true
		}
	}
	return Meta{}, false
}

// Requires returns the full dependency closure of a component (excluding itself),
// in install order (dependencies first).
func Requires(name Name) []Name {
	var out []Name
	seen := map[Name]bool{}
	var walk func(n Name)
	walk = func(n Name) {
		m, ok := Lookup(string(n))
		if !ok {
			return
		}
		for _, dep := range m.Requires {
			if !seen[dep] {
				seen[dep] = true
				walk(dep)
				out = append(out, dep)
			}
		}
	}
	walk(name)
	return out
}

// Dependents returns the components that require the given one (so uninstalling
// it must also remove them).
func Dependents(name Name) []Name {
	var out []Name
	for _, m := range registry {
		for _, dep := range m.Requires {
			if dep == name {
				out = append(out, m.Name)
			}
		}
	}
	return out
}

const registryFile = "components.json"

// State is the persisted set of installed components (components.json).
type State struct {
	Installed map[Name]bool `json:"installed"`
}

// IsInstalled reports whether a component is installed.
func (s State) IsInstalled(name Name) bool {
	return s.Installed != nil && s.Installed[name]
}

func registryPath() (string, error) {
	dir, err := config.DefaultAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, registryFile), nil
}

// Load reads components.json. On first run it auto-migrates: a config dir that
// already holds agent state (an existing monolith user) is treated as having
// every component installed so nothing breaks; a fresh install starts with a
// lean core (no components). The inferred state is persisted.
func Load() (State, error) {
	path, err := registryPath()
	if err != nil {
		return State{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		seed := State{Installed: map[Name]bool{}}
		if existingAgentConfig() {
			for _, m := range registry {
				seed.Installed[m.Name] = true
			}
		}
		_ = save(path, seed) // best-effort; absence just re-seeds next time
		return seed, nil
	}
	if err != nil {
		return State{}, err
	}
	_ = os.Chmod(path, 0o600)

	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	if s.Installed == nil {
		s.Installed = map[Name]bool{}
	}
	return s, nil
}

// Save persists the registry.
func Save(s State) error {
	path, err := registryPath()
	if err != nil {
		return err
	}
	return save(path, s)
}

func save(path string, s State) error {
	if s.Installed == nil {
		s.Installed = map[Name]bool{}
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// existingAgentConfig reports whether the config dir already has agent state,
// used only to migrate pre-component installs (treat them as fully installed).
func existingAgentConfig() bool {
	dir, err := config.DefaultAppDir()
	if err != nil {
		return false
	}
	for _, f := range []string{"agent-allowlist.json", "agent-settings.json", "agent-locations.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}
