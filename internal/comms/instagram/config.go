package instagram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config is the Instagram account configuration, seeded once by the owner at
// ~/.config/zcoms/instagram.json (mode 0600). Instagram's private API has no
// OAuth, so the username/password live here like an account-level secret; the
// derived session is persisted encrypted (see session.go) so we rarely re-login.
//
//	{
//	  "username": "me",
//	  "password": "…",
//	  "proxy": "http://user:pass@host:port",   // optional, recommended
//	  "poll_seconds": 45
//	}
type Config struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	Proxy       string `json:"proxy,omitempty"`
	PollSeconds int    `json:"poll_seconds,omitempty"`
}

// pollInterval is how often the receive loop polls direct threads. Instagram is
// aggressive about automation, so we keep it gentle and never tighter than 20s.
func (c Config) pollInterval() time.Duration {
	s := c.PollSeconds
	if s < 20 {
		s = 45
	}
	return time.Duration(s) * time.Second
}

func (c Config) configured() bool {
	return strings.TrimSpace(c.Username) != "" && strings.TrimSpace(c.Password) != ""
}

// LoadConfig reads instagram.json from dir (~/.config/zcoms). A missing file is
// not an error: the transport stays inert (disconnected) until the owner adds
// credentials, exactly like WhatsApp sits unpaired until the QR is scanned.
func LoadConfig(dir string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(dir, "instagram.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}
