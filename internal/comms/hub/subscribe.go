package hub

import (
	"encoding/json"
	"net"

	"github.com/Zouriel/zcoms/client"
)

// serveSubscription registers a subscriber's event stream for the given role and
// pumps pushed events to it until the client disconnects.
func (d *daemon) serveSubscription(conn net.Conn, role string) {
	if role == "" {
		writeIPC(conn, client.Response{Error: "subscribe needs a role"})
		return
	}
	ch := make(chan client.Event, 64)
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

func (d *daemon) addSubscriber(role string, ch chan client.Event) {
	d.subMu.Lock()
	d.subscribers[role] = append(d.subscribers[role], ch)
	d.subMu.Unlock()
}

func (d *daemon) removeSubscriber(role string, ch chan client.Event) {
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

// broadcast fans an event out to every subscriber across all roles, dropping it
// for any subscriber whose buffer is full so the receive loop never blocks. A
// channel subscribed under multiple roles is only delivered once. Comms is a
// dumb pipe, so it does not route by role — the agent tier decides per message.
func (d *daemon) broadcast(ev client.Event) bool {
	d.subMu.Lock()
	seen := map[chan client.Event]bool{}
	var subs []chan client.Event
	for _, chans := range d.subscribers {
		for _, ch := range chans {
			if !seen[ch] {
				seen[ch] = true
				subs = append(subs, ch)
			}
		}
	}
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

// hasSubscriber reports whether any subscriber is connected for a role.
func (d *daemon) hasSubscriber(role string) bool {
	d.subMu.Lock()
	defer d.subMu.Unlock()
	return len(d.subscribers[role]) > 0
}
