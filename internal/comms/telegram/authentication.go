package telegram

import (
	"encoding/json"
	"fmt"
	"time"
)

type AuthorizationState string

const (
	AuthStateUnknown             AuthorizationState = "unknown"
	AuthStateWaitTdlibParameters AuthorizationState = "authorizationStateWaitTdlibParameters"
	AuthStateWaitPhoneNumber     AuthorizationState = "authorizationStateWaitPhoneNumber"
	AuthStateWaitCode            AuthorizationState = "authorizationStateWaitCode"
	AuthStateWaitPassword        AuthorizationState = "authorizationStateWaitPassword"
	AuthStateReady               AuthorizationState = "authorizationStateReady"
	AuthStateLoggingOut          AuthorizationState = "authorizationStateLoggingOut"
	AuthStateClosing             AuthorizationState = "authorizationStateClosing"
	AuthStateClosed              AuthorizationState = "authorizationStateClosed"
)

func FetchAuthorizationState(tdjson *TDJSON, clientID int32) (AuthorizationState, error) {
	responseJSON, err := SendRequestAndWait(
		tdjson,
		clientID,
		`{"@type":"getAuthorizationState"}`,
		"get-auth-state",
		5*time.Second,
	)
	if err != nil {
		return AuthStateUnknown, err
	}

	var response struct {
		Type string `json:"@type"`
	}
	if err := json.Unmarshal([]byte(responseJSON), &response); err != nil {
		return AuthStateUnknown, err
	}

	if response.Type == "" {
		return AuthStateUnknown, fmt.Errorf("missing @type in response: %s", responseJSON)
	}

	return AuthorizationState(response.Type), nil
}

func ProvideAuthenticationPhoneNumber(tdjson *TDJSON, clientID int32, phoneNumber string) error {
	request := map[string]any{
		"@type":        "setAuthenticationPhoneNumber",
		"phone_number": phoneNumber,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return err
	}

	_, err = SendRequestAndWait(tdjson, clientID, string(requestBytes), "set-phone", 10*time.Second)
	return err
}

func SubmitAuthenticationCode(tdjson *TDJSON, clientID int32, code string) error {
	request := map[string]any{
		"@type": "checkAuthenticationCode",
		"code":  code,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return err
	}

	_, err = SendRequestAndWait(tdjson, clientID, string(requestBytes), "check-code", 10*time.Second)
	return err
}

func SubmitAuthenticationPassword(tdjson *TDJSON, clientID int32, password string) error {
	request := map[string]any{
		"@type":    "checkAuthenticationPassword",
		"password": password,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return err
	}

	_, err = SendRequestAndWait(tdjson, clientID, string(requestBytes), "check-password", 10*time.Second)
	return err
}
func LogOutSession(tdjson *TDJSON, clientID int32) error {
	_, err := SendRequestAndWait(tdjson, clientID, `{"@type":"logOut"}`, "logout", 10*time.Second)
	return err
}
