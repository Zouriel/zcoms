//go:build windows

package agent

import "os/exec"

// hardenProcess is a no-op on Windows (no POSIX process groups). The context
// still kills the direct child process on timeout via CommandContext.
func hardenProcess(cmd *exec.Cmd) {}
