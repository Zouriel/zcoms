package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// parseDisappear reads a disappearing-messages timer: "off" or "0", a number
// of days or weeks ("1d", "7d", "2w"), or a Go duration ("12h").
func parseDisappear(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "off" || s == "0" || s == "never" {
		return 0, nil
	}
	unit := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour}
	if n := len(s); n > 1 {
		if u, ok := unit[s[n-1]]; ok {
			k, err := strconv.Atoi(s[:n-1])
			if err != nil || k <= 0 {
				return 0, fmt.Errorf("bad timer %q (try 1d, 7d, 2w or off)", s)
			}
			return time.Duration(k) * u, nil
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("bad timer %q (try 1d, 7d, 2w or off)", s)
	}
	return d, nil
}

// formatDisappear prints a timer the way parseDisappear reads it.
func formatDisappear(d time.Duration) string {
	day := 24 * time.Hour
	switch {
	case d <= 0:
		return "off"
	case d%(7*day) == 0:
		return fmt.Sprintf("%dw", d/(7*day))
	case d%day == 0:
		return fmt.Sprintf("%dd", d/day)
	}
	return d.String()
}
