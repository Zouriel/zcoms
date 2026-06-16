package agent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// triage-session.json persists the agent session id of the *triage brain* — the
// one long-lived session the scheduled triage pass resumes every run so it
// accumulates memory of everything it has seen. `interact triage` resumes this
// same session. It survives daemon restarts and is only cleared on an explicit
// reset (see ResetTriageSession).
const triageSessionFile = "triage-session.json"

type triageSession struct {
	SessionID string    `json:"session_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// LoadTriageSessionID returns the persisted triage-brain session id, or "" if no
// session has been started yet (or it was reset).
func LoadTriageSessionID() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, triageSessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var s triageSession
	if err := json.Unmarshal(data, &s); err != nil {
		return "", err
	}
	return s.SessionID, nil
}

// SaveTriageSessionID records the triage-brain session id so the next pass (or
// `interact triage`) resumes the same conversation.
func SaveTriageSessionID(id string) error {
	if id == "" {
		return nil
	}
	dir, err := configDir()
	if err != nil {
		return err
	}
	return writeJSON(filepath.Join(dir, triageSessionFile), triageSession{SessionID: id, UpdatedAt: time.Now()})
}

// ResetTriageSession clears the triage brain so the next pass starts fresh with
// no memory of past messages. Missing file is not an error.
func ResetTriageSession() error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, triageSessionFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
