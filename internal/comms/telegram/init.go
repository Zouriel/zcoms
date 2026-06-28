package telegram

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

type ApplyTdlibParametersRequest struct {
	Type                   string `json:"@type"`
	DatabaseDirectory      string `json:"database_directory"`
	FilesDirectory         string `json:"files_directory"`
	UseFileDatabase        bool   `json:"use_file_database"`
	UseChatInfoDatabase    bool   `json:"use_chat_info_database"`
	UseMessageDatabase     bool   `json:"use_message_database"`
	UseSecretChats         bool   `json:"use_secret_chats"`
	ApiID                  int32  `json:"api_id"`
	ApiHash                string `json:"api_hash"`
	SystemLanguageCode     string `json:"system_language_code"`
	DeviceModel            string `json:"device_model"`
	SystemVersion          string `json:"system_version"`
	ApplicationVersion     string `json:"application_version"`
	EnableStorageOptimizer bool   `json:"enable_storage_optimizer"`
}

func platformDeviceModel() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows PC"
	case "darwin":
		return "Mac"
	default:
		return "Linux PC"
	}
}

func EnsureTdlibDirectories(tdlibBaseDirectory string) (string, string, error) {
	databaseDirectory := filepath.Join(tdlibBaseDirectory, "database")
	filesDirectory := filepath.Join(tdlibBaseDirectory, "files")

	if err := os.MkdirAll(databaseDirectory, 0o755); err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filesDirectory, 0o755); err != nil {
		return "", "", err
	}

	return databaseDirectory, filesDirectory, nil
}

func ApplyTdlibParameters(tdjson *TDJSON, clientID int32, tdlibBaseDirectory string, apiID int32, apiHash string) error {
	if apiID == 0 || apiHash == "" {
		return fmt.Errorf("missing credentials: set TG_API_ID and TG_API_HASH")
	}

	databaseDirectory, filesDirectory, err := EnsureTdlibDirectories(tdlibBaseDirectory)
	if err != nil {
		return err
	}

	request := ApplyTdlibParametersRequest{
		Type:                "setTdlibParameters",
		DatabaseDirectory:   databaseDirectory,
		FilesDirectory:      filesDirectory,
		UseFileDatabase:     true,
		UseChatInfoDatabase: true,
		UseMessageDatabase:  true,
		UseSecretChats:      false,

		ApiID:   apiID,
		ApiHash: apiHash,

		SystemLanguageCode:     "en",
		DeviceModel:            platformDeviceModel(),
		SystemVersion:          runtime.GOOS,
		ApplicationVersion:     "0.1",
		EnableStorageOptimizer: true,
	}

	requestBytes, err := json.Marshal(request)
	if err != nil {
		return err
	}

	_, err = SendRequestAndWait(tdjson, clientID, string(requestBytes), "set-tdlib-params", 10*time.Second)
	return err
}
