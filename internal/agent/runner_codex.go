package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// codexSandboxArgs maps a role to Codex's sandbox flags for a fresh session.
func codexSandboxArgs(role Role) []string {
	switch role {
	case RoleFull:
		return []string{"--dangerously-bypass-approvals-and-sandbox"}
	case RoleEdit:
		return []string{"-s", "workspace-write"}
	default: // read, and the confirm "plan" phase
		return []string{"-s", "read-only"}
	}
}

// RunCodex runs one Codex turn (new session or resume) and returns the final
// message plus the thread/session id for follow-ups.
func RunCodex(dir, prompt, resumeID string, role Role, stagingWritable bool) (RunResult, error) {
	lastFile, err := os.CreateTemp("", "codex-last-*.txt")
	if err != nil {
		return RunResult{}, err
	}
	lastPath := lastFile.Name()
	lastFile.Close()
	defer os.Remove(lastPath)

	var args []string
	if resumeID != "" {
		args = append(args, "exec", "resume", resumeID)
	} else {
		args = append(args, "exec")
	}
	args = append(args, "--json", "--skip-git-repo-check", "-o", lastPath)
	switch {
	case stagingWritable:
		// Sandboxed triage/chat agent: dir is a writable scratch space (so it can
		// produce files to SENDFILE) but network stays OFF — it still can't reach
		// Telegram itself. Set via -c so it also applies on `exec resume` (which
		// rejects -s); writable_roots scopes writes to dir, leaving $HOME read-only.
		args = append(args, "-c", `sandbox_mode="workspace-write"`)
		args = append(args, "-c", fmt.Sprintf(`sandbox_workspace_write.writable_roots=["%s"]`, dir))
		if resumeID == "" {
			args = append(args, "-C", dir)
		}
	case resumeID == "":
		args = append(args, "-C", dir)
		args = append(args, codexSandboxArgs(role)...)
	case role == RoleFull:
		// resume doesn't take -s; only the full bypass is settable.
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	// "--" so a prompt starting with "-" isn't parsed as a CLI option.
	args = append(args, "--", prompt)

	ctx, cancel := context.WithTimeout(context.Background(), claudeRunTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, codexBin, args...)
	cmd.Dir = dir
	cmd.Stdin = nil    // don't let codex wait on stdin
	hardenProcess(cmd) // kill the whole child group on timeout (codex spawns helpers)

	stdout := &boundedBuffer{max: maxAgentStdout}
	stderr := &boundedBuffer{max: maxAgentStderr}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return RunResult{}, fmt.Errorf("codex timed out after %s", claudeRunTimeout)
	}

	sessionID := resumeID
	text := ""
	for _, line := range strings.Split(stdout.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Type == "thread.started" && ev.ThreadID != "" {
			sessionID = ev.ThreadID
		}
		if ev.Type == "item.completed" && ev.Item.Type == "agent_message" && ev.Item.Text != "" {
			text = ev.Item.Text
		}
	}

	if fileText, err := os.ReadFile(lastPath); err == nil {
		if s := strings.TrimSpace(string(fileText)); s != "" {
			text = s
		}
	}

	if text == "" {
		if runErr != nil {
			return RunResult{SessionID: sessionID}, fmt.Errorf("codex failed: %v: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return RunResult{SessionID: sessionID}, fmt.Errorf("codex produced no message")
	}

	return RunResult{Text: text, SessionID: sessionID}, nil
}
