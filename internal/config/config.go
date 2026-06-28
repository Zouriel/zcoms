package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// AuthState values for Config.AuthState.
const (
	AuthStateAuthorized   = "authorized"
	AuthStateUnauthorized = "unauthorized"
)

type Config struct {
	TdlibDir    string `json:"tdlib_dir"`
	AuthState   string `json:"auth_state"`
	Username    string `json:"username,omitempty"`
	PhoneNumber string `json:"phone_number,omitempty"`

	// WhatsApp is the optional Baileys sidecar transport. It lives in comms
	// config because WhatsApp is a transport, not an AI concern — the agent tier
	// reaches it through comms like any other channel.
	WhatsApp WhatsAppConfig `json:"whatsapp"`
}

// WhatsAppConfig configures the Baileys sidecar (the second comms transport).
type WhatsAppConfig struct {
	Enabled bool   `json:"enabled"`
	Socket  string `json:"socket"` // path to the sidecar's Unix socket
}

func DefaultAppDir() (string, error) {
	userConfigDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userConfigDirectory, "zcoms"), nil
}

func LoadOrCreate() (Config, string, error) {
	appDirectory, err := DefaultAppDir()
	if err != nil {
		return Config{}, "", err
	}

	if err := os.MkdirAll(appDirectory, 0o755); err != nil {
		return Config{}, "", err
	}

	configFilePath := filepath.Join(appDirectory, "config.json")

	loadedConfiguration, err := loadConfigIfExists(configFilePath)
	if err != nil {
		return Config{}, "", err
	}

	finalConfiguration := applyDefaults(loadedConfiguration, appDirectory)

	if err := os.MkdirAll(finalConfiguration.TdlibDir, 0o755); err != nil {
		return Config{}, "", err
	}

	if err := Save(finalConfiguration, configFilePath); err != nil {
		return Config{}, "", err
	}

	return finalConfiguration, configFilePath, nil
}

func Save(configuration Config, filePath string) error {
	jsonBytes, err := json.MarshalIndent(configuration, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filePath, jsonBytes, 0o644)
}

func loadConfigIfExists(filePath string) (Config, error) {
	fileBytes, err := os.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}

	var configuration Config
	if err := json.Unmarshal(fileBytes, &configuration); err != nil {
		return Config{}, err
	}

	return configuration, nil
}

func applyDefaults(configuration Config, appDirectory string) Config {
	if configuration.TdlibDir == "" {
		configuration.TdlibDir = filepath.Join(appDirectory, "tdlib")
	}

	if configuration.AuthState == "" {
		configuration.AuthState = AuthStateUnauthorized
	}

	if configuration.WhatsApp.Socket == "" {
		configuration.WhatsApp.Socket = filepath.Join(appDirectory, "wa.sock")
	}

	return configuration
}
