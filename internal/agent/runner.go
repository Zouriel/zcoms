package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// claudeRunTimeout bounds a single Claude turn so a stuck run can't wedge a
// user's session forever.
const claudeRunTimeout = 20 * time.Minute

// Caps on captured agent output so a runaway/looping CLI can't grow the
// long-lived daemon's memory without bound.
const (
	maxAgentStdout = 16 << 20 // 16 MiB
	maxAgentStderr = 1 << 20  // 1 MiB
)

// boundedBuffer collects up to max bytes and silently discards the rest, while
// reporting full writes so the child process never blocks or sees EPIPE.
type boundedBuffer struct {
	b   strings.Builder
	max int
}

func (w *boundedBuffer) Write(p []byte) (int, error) {
	if room := w.max - w.b.Len(); room > 0 {
		if room < len(p) {
			w.b.Write(p[:room])
		} else {
			w.b.Write(p)
		}
	}
	return len(p), nil
}

func (w *boundedBuffer) String() string { return w.b.String() }

// Backend selects which agent CLI drives a session.
type Backend string

const (
	BackendClaude Backend = "claude"
	BackendCodex  Backend = "codex"
)

func (b Backend) normalize() Backend {
	if b == BackendCodex {
		return BackendCodex
	}
	return BackendClaude
}

// agentBin resolves a CLI's absolute path so the daemon works even when launched
// (e.g. by systemd) with a PATH that omits ~/.local/bin or /usr/bin.
func agentBin(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".local", "bin", name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return name
}

var (
	claudeBin = agentBin("claude")
	codexBin  = agentBin("codex")
)

// RunAgent runs one turn with the chosen backend. When stagingWritable is set,
// the (otherwise read-only) sandboxed agent gets dir as a writable scratch space
// — still no network — so it can produce files to SENDFILE.
func RunAgent(backend Backend, dir, prompt, resumeID string, role Role, stagingWritable bool) (RunResult, error) {
	if backend.normalize() == BackendCodex {
		return RunCodex(dir, prompt, resumeID, role, stagingWritable)
	}
	return RunClaude(dir, prompt, resumeID, role, stagingWritable)
}

// RunResult is the outcome of one Claude turn.
type RunResult struct {
	Text      string
	SessionID string
}

// claudeJSON is the shape of `claude -p --output-format json`.
type claudeJSON struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	SessionID string `json:"session_id"`
}

// permissionArgs maps a role to the claude flags that let it run unattended.
func permissionArgs(role Role) []string {
	switch role {
	case RoleFull:
		return []string{"--dangerously-skip-permissions"}
	case RoleEdit:
		return []string{"--permission-mode", "acceptEdits"}
	case RoleRead:
		return []string{"--permission-mode", "plan"}
	default:
		return []string{"--permission-mode", "plan"}
	}
}

// RunClaude runs one Claude turn in dir. If resumeID is set it continues that
// session; otherwise it starts a new one. It returns the reply text and the
// session id (new or unchanged) for follow-up turns.
func RunClaude(dir, prompt, resumeID string, role Role, stagingWritable bool) (RunResult, error) {
	args := []string{"-p", "--output-format", "json"}
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}
	if stagingWritable {
		// Let the read-role agent write to its staging dir; claude plan mode would
		// otherwise block file creation.
		args = append(args, "--permission-mode", "acceptEdits")
	} else {
		args = append(args, permissionArgs(role)...)
	}
	// "--" so a prompt starting with "-" isn't parsed as a CLI option.
	args = append(args, "--", prompt)

	ctx, cancel := context.WithTimeout(context.Background(), claudeRunTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, claudeBin, args...)
	cmd.Dir = dir
	cmd.Stdin = nil    // don't let claude wait on stdin
	hardenProcess(cmd) // kill the whole child group on timeout (claude spawns MCP/node)

	stdout := &boundedBuffer{max: maxAgentStdout}
	stderr := &boundedBuffer{max: maxAgentStderr}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return RunResult{}, fmt.Errorf("claude timed out after %s", claudeRunTimeout)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		if err != nil {
			return RunResult{}, fmt.Errorf("claude failed: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
		return RunResult{}, fmt.Errorf("claude produced no output: %s", strings.TrimSpace(stderr.String()))
	}

	var parsed claudeJSON
	if jsonErr := json.Unmarshal([]byte(out), &parsed); jsonErr != nil {
		// Couldn't parse — surface whatever we got so the user isn't left blank.
		return RunResult{}, fmt.Errorf("could not parse claude output: %v", jsonErr)
	}

	if parsed.IsError {
		text := parsed.Result
		if text == "" {
			text = "Claude reported an error."
		}
		return RunResult{Text: text, SessionID: parsed.SessionID}, fmt.Errorf("claude error: %s", parsed.Subtype)
	}

	return RunResult{Text: parsed.Result, SessionID: parsed.SessionID}, nil
}
