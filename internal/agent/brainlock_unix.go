//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"syscall"
)

// lockTriageBrain takes a blocking, cross-process lock on the triage-brain
// session, the same flock the external triage component (zcoms-sdk) uses, so a
// scheduled triage pass and an interactive `interact triage` turn never drive
// the shared agent session at once. Returns an unlock func (nil + fail-open on
// error). The lock auto-releases if the holder process dies.
func lockTriageBrain() func() {
	dir, err := configDir()
	if err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "triage-brain.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
