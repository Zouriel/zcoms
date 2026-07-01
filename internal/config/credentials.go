package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var (
	BuildAPIID   string
	BuildAPIHash string
)

var ErrMissingCredentials = errors.New("missing TG_API_ID or TG_API_HASH")

func ResolveAPICredentials() (int32, string, error) {
	apiIDText := strings.TrimSpace(os.Getenv("TG_API_ID"))
	apiHash := strings.TrimSpace(os.Getenv("TG_API_HASH"))

	if apiIDText == "" || apiHash == "" {
		apiIDText = strings.TrimSpace(BuildAPIID)
		apiHash = strings.TrimSpace(BuildAPIHash)
	}

	if apiIDText == "" || apiHash == "" {
		return 0, "", ErrMissingCredentials
	}

	parsedID, err := strconv.ParseInt(apiIDText, 10, 32)
	if err != nil {
		return 0, "", fmt.Errorf("TG_API_ID must be a number")
	}

	return int32(parsedID), apiHash, nil
}
