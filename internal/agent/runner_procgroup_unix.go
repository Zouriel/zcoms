//go:build !windows

package agent

import (
	"os/exec"
	"syscall"
	"time"
)

// hardenProcess puts the agent CLI in its own process group and kills the WHOLE
// group if the run's context is cancelled (timeout). claude/codex spawn helper
// processes (node, MCP servers, sandbox helpers) that would otherwise survive as
// orphans and accumulate in the long-running daemon.
func hardenProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// Negative pid targets the whole process group.
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	// If pipes stay open after the group is signalled, don't wait forever.
	cmd.WaitDelay = 5 * time.Second
}
