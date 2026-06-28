package client

import (
	"os"
	"path/filepath"
)

// DefaultAppDir is the zcoms config/state directory (~/.config/zcoms on Linux).
// It is the single canonical resolver shared by all tiers — the daemon, the
// agent layer, and modules all read and write the same directory.
func DefaultAppDir() (string, error) {
	userConfigDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userConfigDirectory, "zcoms"), nil
}
