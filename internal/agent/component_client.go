package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"time"
)

// ComponentCommand forwards a command line to a component listening on
// <socketFile> in the config dir (e.g. "team.sock") and returns its reply.
// handled is false when the component isn't running. actor is who issued it
// (an @username), recorded by the component for audit.
func ComponentCommand(socketFile, text, actor string) (handled bool, reply string, err error) {
	dir, derr := configDir()
	if derr != nil {
		return false, "", derr
	}
	conn, derr := net.DialTimeout("unix", filepath.Join(dir, socketFile), 2*time.Second)
	if derr != nil {
		return false, "", nil
	}
	defer conn.Close()

	req, _ := json.Marshal(struct {
		Text  string `json:"text"`
		Actor string `json:"actor,omitempty"`
	}{text, actor})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return true, "", err
	}
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	line, rerr := bufio.NewReader(conn).ReadBytes('\n')
	if rerr != nil && len(line) == 0 {
		return true, "", rerr
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Reply string `json:"reply"`
		Error string `json:"error"`
	}
	if json.Unmarshal(line, &resp) != nil {
		return true, "", errors.New("bad component response")
	}
	if !resp.OK {
		return true, "", errors.New(resp.Error)
	}
	return true, resp.Reply, nil
}
