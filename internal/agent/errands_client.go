package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"time"
)

// ErrandsCommand forwards an "errand …" command line to the external errands
// component over its socket (errands.sock) and returns its reply. handled is
// false when the component isn't running (so callers can say so).
func ErrandsCommand(text string) (handled bool, reply string, err error) {
	dir, derr := configDir()
	if derr != nil {
		return false, "", derr
	}
	conn, derr := net.DialTimeout("unix", filepath.Join(dir, "errands.sock"), 2*time.Second)
	if derr != nil {
		return false, "", nil // not running → caller reports unavailable
	}
	defer conn.Close()

	req, _ := json.Marshal(struct {
		Text string `json:"text"`
	}{text})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return true, "", err
	}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
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
		return true, "", errors.New("bad errands response")
	}
	if !resp.OK {
		return true, "", errors.New(resp.Error)
	}
	return true, resp.Reply, nil
}
