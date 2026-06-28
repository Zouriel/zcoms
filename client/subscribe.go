package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
)

// Subscribe opens a streaming subscription for role (a module/role name like
// "bridge" or "errands") and calls handler for each incoming message event until
// the connection drops (then it returns the error). Callers typically run this
// in a reconnect loop — see Harness.Run.
func (c *Client) Subscribe(role string, handler func(Event)) error {
	conn, err := net.Dial("unix", c.socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	line, _ := json.Marshal(Request{Op: "subscribe", Role: role, Version: ProtocolVersion})
	if _, err := conn.Write(append(line, '\n')); err != nil {
		return err
	}
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev Event
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Event == "message" {
			handler(ev)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("subscription closed by daemon")
}
