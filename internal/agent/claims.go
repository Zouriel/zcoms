package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// claims is the read side of claims.json (written by the external errands
// component): the chats it currently owns, so the daemon routes their incoming
// messages to the errands subscriber and excludes them from the triage `unread`
// op. JSON-compatible with zcoms-sdk/agent.Claims.
type claims struct {
	TG []int64  `json:"tg"`
	WA []string `json:"wa"`
}

func (c claims) hasTG(id int64) bool {
	for _, x := range c.TG {
		if x == id {
			return true
		}
	}
	return false
}

// loadClaims reads claims.json (missing => empty).
func loadClaims() claims {
	dir, err := configDir()
	if err != nil {
		return claims{}
	}
	data, err := os.ReadFile(filepath.Join(dir, "claims.json"))
	if err != nil {
		return claims{}
	}
	var c claims
	_ = json.Unmarshal(data, &c)
	return c
}
