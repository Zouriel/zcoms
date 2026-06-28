package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"time"
)

// ModuleResult is a reply from a module/agent command socket.
type ModuleResult struct {
	Reply    string
	Continue bool
	Running  bool // false when the module isn't listening on its socket
}

// ModuleCommand forwards a command line to a module/agent listening on
// <socketFile> in the app dir (e.g. "agent.sock", "team.sock") and returns its
// reply. Running is false when nothing is listening, so the CLI can say
// "install it" instead of erroring. This is the generic module router seam: a
// `zc` verb that drives the agent or a module is a ~3-line pass-through to here.
func ModuleCommand(socketFile, text, actor string) (ModuleResult, error) {
	dir, err := DefaultAppDir()
	if err != nil {
		return ModuleResult{}, err
	}
	conn, err := net.DialTimeout("unix", filepath.Join(dir, socketFile), 2*time.Second)
	if err != nil {
		return ModuleResult{Running: false}, nil
	}
	defer conn.Close()

	req, _ := json.Marshal(struct {
		Text  string `json:"text"`
		Actor string `json:"actor,omitempty"`
	}{text, actor})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return ModuleResult{Running: true}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	line, rerr := bufio.NewReader(conn).ReadBytes('\n')
	if rerr != nil && len(line) == 0 {
		return ModuleResult{Running: true}, rerr
	}
	var resp struct {
		OK       bool   `json:"ok"`
		Reply    string `json:"reply"`
		Continue bool   `json:"continue"`
		Error    string `json:"error"`
	}
	if json.Unmarshal(line, &resp) != nil {
		return ModuleResult{Running: true}, errors.New("bad module response")
	}
	if !resp.OK {
		return ModuleResult{Running: true}, errors.New(resp.Error)
	}
	return ModuleResult{Reply: resp.Reply, Continue: resp.Continue, Running: true}, nil
}
