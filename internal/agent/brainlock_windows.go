//go:build windows

package agent

// lockTriageBrain is a no-op on Windows (the daemon runs on Linux).
func lockTriageBrain() func() { return func() {} }
