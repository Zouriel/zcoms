package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
)

// Unified config: one ~/.config/zcoms/config.json with a top-level section per
// concern. This mirrors the SDK's accessor (same file, lock, detection, and
// legacy mapping) so the daemon and the pure-Go components share one format.

const configFileName = "config.json"

var legacySections = map[string]string{
	"settings":   "agent-settings.json",
	"agents":     "agents.json",
	"allowlist":  "agent-allowlist.json",
	"locations":  "agent-locations.json",
	"components": "components.json",
}

func configFilePath() (string, error) {
	dir, err := DefaultAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, configFileName), nil
}

func lockConfig() func() {
	dir, err := DefaultAppDir()
	if err != nil {
		return func() {}
	}
	f, err := os.OpenFile(filepath.Join(dir, "config.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX) != nil {
		f.Close()
		return func() {}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

func readRawConfig() map[string]json.RawMessage {
	m := map[string]json.RawMessage{}
	p, err := configFilePath()
	if err != nil {
		return m
	}
	if data, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func writeRawConfig(m map[string]json.RawMessage) error {
	p, err := configFilePath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func ensureMigrated() {
	unlock := lockConfig()
	defer unlock()
	raw := readRawConfig()
	if _, legacy := raw["tdlib_dir"]; !legacy {
		return
	}
	unified := map[string]json.RawMessage{}
	if b, err := json.Marshal(raw); err == nil {
		unified["core"] = b
	}
	dir, _ := DefaultAppDir()
	for key, fname := range legacySections {
		if data, err := os.ReadFile(filepath.Join(dir, fname)); err == nil {
			unified[key] = json.RawMessage(data)
		}
	}
	_ = writeRawConfig(unified)
}

// GetSection unmarshals one config section into dest (found=false if absent).
func GetSection(key string, dest any) (found bool, err error) {
	ensureMigrated()
	raw := readRawConfig()
	b, ok := raw[key]
	if !ok || len(b) == 0 {
		return false, nil
	}
	return true, json.Unmarshal(b, dest)
}

// PutSection writes one config section, preserving the rest of the file.
func PutSection(key string, value any) error {
	ensureMigrated()
	unlock := lockConfig()
	defer unlock()
	raw := readRawConfig()
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw[key] = b
	return writeRawConfig(raw)
}
