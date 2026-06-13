// Package agent implements the `tg daemon` bridge: it listens on the logged-in
// Telegram account for messages from allow-listed users and drives Claude Code
// sessions on their behalf (pick a location, resume a session, chat back/forth).
package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"tg/internal/config"
)

// Role controls how much an allow-listed user can make Claude do.
type Role string

const (
	RoleFull    Role = "full"    // can do anything (--dangerously-skip-permissions)
	RoleEdit    Role = "edit"    // read/write/run, auto-approved (acceptEdits)
	RoleConfirm Role = "confirm" // plans first, executes only after you approve in Telegram
	RoleRead    Role = "read"    // inspect/plan only, never acts (plan mode)
)

// rank orders roles from least to most powerful, for capping.
func (r Role) rank() int {
	switch r {
	case RoleRead:
		return 1
	case RoleConfirm:
		return 2
	case RoleEdit:
		return 3
	case RoleFull:
		return 4
	}
	return 0
}

func (r Role) valid() bool { return r.rank() > 0 }

// ValidRole reports whether r is a known role (read|confirm|edit|full).
func ValidRole(r Role) bool { return r.valid() }

// MinRole returns the more restrictive (less powerful) of two roles.
func MinRole(a, b Role) Role {
	if a.rank() <= b.rank() {
		return a
	}
	return b
}

// AllowEntry is one allow-listed user's permissions.
type AllowEntry struct {
	Role      Role     `json:"role"`
	Locations []string `json:"locations"`       // location names, or ["*"] for all
	Agent     Backend  `json:"agent,omitempty"` // "claude" (default) | "codex"

	UserID int64 `json:"-"` // resolved from the @username at startup
}

// AllowsLocation reports whether this entry may use the named location.
func (e AllowEntry) AllowsLocation(name string) bool {
	for _, l := range e.Locations {
		if l == "*" || l == name {
			return true
		}
	}
	return false
}

// Allowlist maps @username -> permissions.
type Allowlist map[string]AllowEntry

// LocationConfig is a project directory plus an optional ceiling on what any
// user may do there (e.g. a production repo capped to "read" regardless of role).
type LocationConfig struct {
	Path    string `json:"path"`
	MaxRole Role   `json:"max_role,omitempty"`
}

// Locations maps a friendly name -> location config. In JSON each value may be
// either a plain path string or an object {"path": ..., "max_role": ...}.
type Locations map[string]LocationConfig

// MarshalJSON writes a plain path string when there's no cap, else an object,
// keeping agent-locations.json tidy.
func (l Locations) MarshalJSON() ([]byte, error) {
	out := map[string]any{}
	for name, cfg := range l {
		if cfg.MaxRole == "" {
			out[name] = cfg.Path
		} else {
			out[name] = cfg
		}
	}
	return json.Marshal(out)
}

func (l *Locations) UnmarshalJSON(data []byte) error {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := Locations{}
	for name, value := range raw {
		var asString string
		if json.Unmarshal(value, &asString) == nil {
			out[name] = LocationConfig{Path: asString}
			continue
		}
		var cfg LocationConfig
		if err := json.Unmarshal(value, &cfg); err != nil {
			return err
		}
		out[name] = cfg
	}
	*l = out
	return nil
}

const (
	locationsFile = "agent-locations.json"
	allowlistFile = "agent-allowlist.json"
)

func configDir() (string, error) {
	return config.DefaultAppDir()
}

// LoadOrSeedLocations reads agent-locations.json, creating a placeholder file on
// first run so the user has something to edit.
func LoadOrSeedLocations() (Locations, string, error) {
	dir, err := configDir()
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, locationsFile)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		seed := Locations{
			"example":       {Path: "/absolute/path/to/a/project"},
			"prod-readonly": {Path: "/absolute/path/to/prod", MaxRole: RoleRead},
		}
		if err := writeJSON(path, seed); err != nil {
			return nil, path, err
		}
		return seed, path, nil
	}
	if err != nil {
		return nil, path, err
	}

	_ = os.Chmod(path, 0o600) // may contain paths you'd rather not expose to other local users

	var locations Locations
	if err := json.Unmarshal(data, &locations); err != nil {
		return nil, path, err
	}
	return locations, path, nil
}

// LoadOrSeedAllowlist reads agent-allowlist.json, creating a placeholder on first
// run. Entries with an invalid/empty role are dropped.
func LoadOrSeedAllowlist() (Allowlist, string, error) {
	dir, err := configDir()
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, allowlistFile)

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		seed := Allowlist{"@your_username": {Role: RoleFull, Locations: []string{"*"}}}
		if err := writeJSON(path, seed); err != nil {
			return nil, path, err
		}
		return seed, path, nil
	}
	if err != nil {
		return nil, path, err
	}

	_ = os.Chmod(path, 0o600) // the allowlist decides who can drive Claude — keep it owner-only

	var raw Allowlist
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, path, err
	}

	cleaned := Allowlist{}
	for username, entry := range raw {
		if !entry.Role.valid() {
			continue
		}
		if len(entry.Locations) == 0 {
			entry.Locations = []string{"*"}
		}
		cleaned[username] = entry
	}
	return cleaned, path, nil
}

// SaveLocations writes agent-locations.json.
func SaveLocations(l Locations) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, locationsFile)
	return path, writeJSON(path, l)
}

// SaveAllowlist writes agent-allowlist.json.
func SaveAllowlist(a Allowlist) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, allowlistFile)
	return path, writeJSON(path, a)
}

// SortedLocationNames returns location names in stable alphabetical order so the
// numbered menu is consistent between calls.
func (l Locations) SortedNames() []string {
	names := make([]string, 0, len(l))
	for name := range l {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
