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
}

func DefaultAppDir() (string, error) {
	userConfigDirectory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(userConfigDirectory, "tg"), nil
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

	return configuration
}
