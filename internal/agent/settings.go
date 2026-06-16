package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TriageSchedules are the schedules `zc triage` accepts.
var TriageSchedules = []string{"30m", "1h", "2h", "3h", "6h", "12h", "twice-daily"}

// TriageSettings configures the importance triage of inbound messages from
// people who aren't on the allow-list.
type TriageSettings struct {
	Enabled     bool   `json:"enabled"`
	Dir         string `json:"dir"`      // working dir for the triage agent
	Schedule    string `json:"schedule"` // 30m | 1h | 2h | 3h | 6h | 12h | twice-daily
	MorningHour int    `json:"morning_hour,omitempty"`
	NightHour   int    `json:"night_hour,omitempty"`
	// The agent backend for triage is configured in agents.json (tasks.triage).

	EveryMinutes int `json:"every_minutes,omitempty"` // legacy fallback
}

// ValidTriageSchedule reports whether s is an accepted schedule.
func ValidTriageSchedule(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	for _, v := range TriageSchedules {
		if v == s {
			return true
		}
	}
	return false
}

func (t TriageSettings) isTwiceDaily() bool {
	s := strings.ToLower(strings.TrimSpace(t.Schedule))
	return s == "twice-daily" || s == "morning-night"
}

func (t TriageSettings) morningHour() int {
	if t.MorningHour >= 0 && t.MorningHour <= 23 && t.MorningHour != 0 {
		return t.MorningHour
	}
	return 8
}

func (t TriageSettings) nightHour() int {
	if t.NightHour >= 0 && t.NightHour <= 23 && t.NightHour != 0 {
		return t.NightHour
	}
	return 22
}

// interval returns the polling interval for the non-twice-daily schedules.
func (t TriageSettings) interval() time.Duration {
	if s := strings.TrimSpace(t.Schedule); s != "" {
		if d, err := time.ParseDuration(s); err == nil && d > 0 {
			return d
		}
	}
	if t.EveryMinutes > 0 {
		return time.Duration(t.EveryMinutes) * time.Minute
	}
	return time.Hour
}

// NextRun returns the next time triage should fire after now.
func (t TriageSettings) NextRun(now time.Time) time.Time {
	if t.isTwiceDaily() {
		m := time.Date(now.Year(), now.Month(), now.Day(), t.morningHour(), 0, 0, 0, now.Location())
		n := time.Date(now.Year(), now.Month(), now.Day(), t.nightHour(), 0, 0, 0, now.Location())
		for _, c := range []time.Time{m, n} {
			if c.After(now) {
				return c
			}
		}
		// both passed today -> tomorrow morning
		return time.Date(now.Year(), now.Month(), now.Day()+1, t.morningHour(), 0, 0, 0, now.Location())
	}
	return now.Add(t.interval())
}

// Describe returns a human description of the schedule.
func (t TriageSettings) Describe() string {
	if t.isTwiceDaily() {
		return fmt.Sprintf("twice daily (%02d:00 and %02d:00)", t.morningHour(), t.nightHour())
	}
	return "every " + t.interval().String()
}

// WhatsAppSettings configures the optional Baileys sidecar that lets triage
// (and `interact triage`) read and reply to WhatsApp alongside Telegram. It is
// disabled by default: with Enabled=false the daemon never touches the socket
// and behavior is identical to a Telegram-only build.
type WhatsAppSettings struct {
	Enabled         bool   `json:"enabled"`
	Socket          string `json:"socket"`             // path to the sidecar's Unix socket
	MarkReadOnReply bool   `json:"mark_read_on_reply"` // mark a WA thread read when the agent replies (default off)
	ReadReceipts    bool   `json:"read_receipts"`      // let triage send WA read receipts/blue ticks (default off: read silently)
}

// Settings drives the auto-reply and triage features (agent-settings.json).
type Settings struct {
	MainUser         string           `json:"main_user"`          // @username to notify / send digests to
	AutoReplyEnabled bool             `json:"auto_reply_enabled"` // reply to non-allow-listed senders
	AutoReply        string           `json:"auto_reply"`         // the canned reply text
	Triage           TriageSettings   `json:"triage"`
	WhatsApp         WhatsAppSettings `json:"whatsapp"`
}

const settingsFile = "agent-settings.json"

// LoadOrSeedSettings reads agent-settings.json, creating a disabled placeholder
// on first run.
func LoadOrSeedSettings() (Settings, string, error) {
	dir, err := configDir()
	if err != nil {
		return Settings{}, "", err
	}
	path := filepath.Join(dir, settingsFile)

	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		seed := Settings{
			MainUser:         "@your_username",
			AutoReplyEnabled: false,
			AutoReply:        "Message received — the owner will be notified shortly.",
			Triage: TriageSettings{
				Enabled:  false,
				Schedule: "1h",
				Dir:      home,
			},
			WhatsApp: WhatsAppSettings{
				Enabled:         false,
				Socket:          filepath.Join(dir, "wa.sock"),
				MarkReadOnReply: false,
				ReadReceipts:    false,
			},
		}
		if err := writeJSON(path, seed); err != nil {
			return Settings{}, path, err
		}
		return seed, path, nil
	}
	if err != nil {
		return Settings{}, path, err
	}

	_ = os.Chmod(path, 0o600)

	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, path, err
	}
	if settings.Triage.Schedule == "" && settings.Triage.EveryMinutes == 0 {
		settings.Triage.Schedule = "1h"
	}
	if settings.Triage.Dir == "" {
		settings.Triage.Dir = home
	}
	if settings.WhatsApp.Socket == "" {
		settings.WhatsApp.Socket = filepath.Join(dir, "wa.sock")
	}
	return settings, path, nil
}

// SaveSettings writes agent-settings.json.
func SaveSettings(s Settings) (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, settingsFile)
	return path, writeJSON(path, s)
}
