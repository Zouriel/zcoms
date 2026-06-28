package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type tdlibError struct {
	Type    string `json:"@type"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type envelope struct {
	Type  string          `json:"@type"`
	Extra json.RawMessage `json:"@extra,omitempty"`
}

// receiveMu serializes the raw td_receive call across dispatchers. The tdjson
// API expects td_receive to be called from a single thread at a time; during
// the brief window where an old dispatcher is shutting down and a new one is
// starting (the login re-open path) this keeps the two from racing.
var receiveMu sync.Mutex

// dispatcher owns the single Receive loop for a TDJSON client. Every event read
// from td_receive is either a reply to an in-flight request (matched by its
// @extra tag) and handed to the waiting caller, or an unsolicited update routed
// to the updates channel. This is what prevents request/reply traffic from
// swallowing incoming message updates (and vice-versa).
type dispatcher struct {
	tdjson *TDJSON

	mu      sync.Mutex
	pending map[string]chan string

	updates  chan string
	stopCh   chan struct{}
	stopOnce sync.Once
	seq      uint64
}

var (
	dispatchersMu sync.Mutex
	dispatchers   = map[*TDJSON]*dispatcher{}
)

func dispatcherFor(tdjson *TDJSON) *dispatcher {
	dispatchersMu.Lock()
	defer dispatchersMu.Unlock()

	if d, ok := dispatchers[tdjson]; ok {
		return d
	}

	d := &dispatcher{
		tdjson:  tdjson,
		pending: map[string]chan string{},
		updates: make(chan string, 1024),
		stopCh:  make(chan struct{}),
	}
	dispatchers[tdjson] = d
	go d.receiveLoop()
	return d
}

func stopDispatcherFor(tdjson *TDJSON) {
	dispatchersMu.Lock()
	d, ok := dispatchers[tdjson]
	if ok {
		delete(dispatchers, tdjson)
	}
	dispatchersMu.Unlock()

	if ok {
		d.stop()
	}
}

func (d *dispatcher) stop() {
	d.stopOnce.Do(func() { close(d.stopCh) })
}

func (d *dispatcher) receiveLoop() {
	for {
		select {
		case <-d.stopCh:
			return
		default:
		}

		receiveMu.Lock()
		raw, err := d.tdjson.Receive(1.0)
		receiveMu.Unlock()

		if err != nil {
			select {
			case <-d.stopCh:
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		if raw == "" {
			continue
		}

		var env envelope
		if json.Unmarshal([]byte(raw), &env) == nil && len(env.Extra) > 0 {
			var tag string
			if json.Unmarshal(env.Extra, &tag) == nil && tag != "" {
				d.mu.Lock()
				ch, ok := d.pending[tag]
				if ok {
					delete(d.pending, tag)
				}
				d.mu.Unlock()
				if ok {
					ch <- raw // buffered (cap 1), never blocks
					continue
				}
			}
		}

		// Unsolicited update. Deliver to the updates channel; if no consumer is
		// keeping up, drop the oldest so the newest is always available.
		select {
		case d.updates <- raw:
		default:
			select {
			case <-d.updates:
			default:
			}
			select {
			case d.updates <- raw:
			default:
			}
		}
	}
}

func (d *dispatcher) request(clientID int32, requestJSON, extraTag string, timeout time.Duration) (string, error) {
	var requestMap map[string]any
	if err := json.Unmarshal([]byte(requestJSON), &requestMap); err != nil {
		return "", err
	}
	requestMap["@extra"] = extraTag

	requestBytes, err := json.Marshal(requestMap)
	if err != nil {
		return "", err
	}

	resultCh := make(chan string, 1)
	d.mu.Lock()
	d.pending[extraTag] = resultCh
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.pending, extraTag)
		d.mu.Unlock()
	}()

	if err := d.tdjson.Send(clientID, string(requestBytes)); err != nil {
		return "", err
	}

	select {
	case raw := <-resultCh:
		var env envelope
		if json.Unmarshal([]byte(raw), &env) == nil && env.Type == "error" {
			var tdErr tdlibError
			if json.Unmarshal([]byte(raw), &tdErr) == nil {
				return "", fmt.Errorf("tdlib error %d: %s", tdErr.Code, tdErr.Message)
			}
			return "", fmt.Errorf("tdlib error: %s", raw)
		}
		return raw, nil
	case <-time.After(timeout):
		return "", errors.New("timeout waiting for TDLib response")
	case <-d.stopCh:
		return "", errors.New("TDLib client closed")
	}
}

// SendRequestAndWait sends a request and blocks until the matching reply (by
// @extra) arrives, the timeout elapses, or the client is closed. The extraTag
// is suffixed with a per-client sequence number so concurrent callers reusing
// the same logical tag (e.g. "get-user") never collide on the pending map.
func SendRequestAndWait(tdjson *TDJSON, clientID int32, requestJSON string, extraTag string, timeout time.Duration) (string, error) {
	d := dispatcherFor(tdjson)
	tag := fmt.Sprintf("%s-%d", extraTag, atomic.AddUint64(&d.seq, 1))
	return d.request(clientID, requestJSON, tag, timeout)
}
