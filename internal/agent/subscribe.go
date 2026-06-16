package agent

import (
	"encoding/json"
	"net"
)

// serveSubscription registers a component's event stream for the given role and
// pumps pushed events to it until the client disconnects. The daemon never
// blocks on a slow subscriber (pushEvent drops when the buffer is full).
func (d *daemon) serveSubscription(conn net.Conn, role string) {
	if role == "" {
		writeIPC(conn, ipcResponse{Error: "subscribe needs a role"})
		return
	}
	ch := make(chan ipcEvent, 64)
	d.addSubscriber(role, ch)
	defer d.removeSubscriber(role, ch)

	// Detect the client going away so we can stop pushing to a dead conn.
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1)
		for {
			if _, err := conn.Read(buf); err != nil {
				close(done)
				return
			}
		}
	}()

	for {
		select {
		case ev := <-ch:
			line, _ := json.Marshal(ev)
			if _, err := conn.Write(append(line, '\n')); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}

func (d *daemon) addSubscriber(role string, ch chan ipcEvent) {
	d.subMu.Lock()
	d.subscribers[role] = append(d.subscribers[role], ch)
	d.subMu.Unlock()
}

func (d *daemon) removeSubscriber(role string, ch chan ipcEvent) {
	d.subMu.Lock()
	subs := d.subscribers[role]
	for i, c := range subs {
		if c == ch {
			d.subscribers[role] = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	d.subMu.Unlock()
}

// pushEvent fans an event out to every subscriber of a role, dropping it for any
// subscriber whose buffer is full so the receive loop is never blocked.
func (d *daemon) pushEvent(role string, ev ipcEvent) bool {
	d.subMu.Lock()
	subs := append([]chan ipcEvent(nil), d.subscribers[role]...)
	d.subMu.Unlock()
	delivered := false
	for _, ch := range subs {
		select {
		case ch <- ev:
			delivered = true
		default: // slow consumer — drop rather than block the daemon
		}
	}
	return delivered
}

// hasSubscriber reports whether any component is subscribed for a role.
func (d *daemon) hasSubscriber(role string) bool {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	return len(d.subscribers[role]) > 0
}
