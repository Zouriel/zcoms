package agent

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"zcoms/internal/tdlib"
)

// isCommerceCommand reports whether a Telegram message looks like a commerce
// command. Besides the explicit `commerce …` prefix it recognises the obvious
// control-plane verbs so the owner can type `store list` or `new store …`
// without the prefix once they know the surface.
func isCommerceCommand(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch lower {
	case "commerce", "store list", "stores", "products", "new store":
		return true
	}
	for _, prefix := range []string{"commerce ", "store ", "stores ", "product ", "products ", "billing ", "report "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// shouldRouteCommerce decides whether an incoming message should be handed to
// the commerce component. Commerce is an admin control plane (stores, billing),
// so only the owner and allow-listed users may drive it from Telegram; everyone
// else falls through to the normal bridge/triage path. A user mid-conversation
// (the component asked a follow-up) keeps routing there regardless of keywords.
func (d *daemon) shouldRouteCommerce(userID int64, text string) bool {
	if !d.commerceInstalled {
		return false
	}
	d.mu.Lock()
	_, allowed := d.byUser[userID]
	inSession := d.commerceSessions[userID]
	d.mu.Unlock()
	if userID != d.mainUserID && !allowed {
		return false
	}
	return inSession || isCommerceCommand(text)
}

// commercePayload strips the `commerce` prefix so the component receives the
// same command line the `zc commerce` CLI would send (e.g. "store list"). A
// bare "commerce" maps to the component's help.
func commercePayload(text string) string {
	t := strings.TrimSpace(text)
	if strings.EqualFold(t, "commerce") {
		return "help"
	}
	const p = "commerce "
	if len(t) >= len(p) && strings.EqualFold(t[:len(p)], p) {
		return strings.TrimSpace(t[len(p):])
	}
	return t
}

func (d *daemon) routeToCommerce(msg tdlib.Message, text string) {
	actor := d.telegramActor(msg.SenderID.UserID)
	reply, cont, err := d.commerceCommand(commercePayload(text), actor)
	if err != nil {
		d.setCommerceSession(msg.SenderID.UserID, false)
		d.send(msg.ChatID, "⚠️ "+err.Error())
		return
	}
	d.setCommerceSession(msg.SenderID.UserID, cont)
	if reply != "" {
		d.send(msg.ChatID, reply)
	}
	if err := tdlib.MarkMessagesRead(d.tdjson, d.clientID, msg.ChatID, []int64{msg.ID}); err != nil {
		fmt.Printf("[commerce] couldn't mark message read: %v\n", err)
	}
}

func (d *daemon) setCommerceSession(userID int64, on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if on {
		d.commerceSessions[userID] = true
	} else {
		delete(d.commerceSessions, userID)
	}
}

// commerceCommand forwards a command line to the commerce component over its
// IPC socket. It mirrors teamCommand: the optional `continue` flag lets the
// component hold a multi-turn conversation (e.g. a guided store-creation flow);
// components that don't send it simply behave as single-shot.
func (d *daemon) commerceCommand(text, actor string) (reply string, cont bool, err error) {
	dir, err := configDir()
	if err != nil {
		return "", false, err
	}
	conn, err := net.DialTimeout("unix", filepath.Join(dir, "commerce.sock"), 2*time.Second)
	if err != nil {
		return "", false, fmt.Errorf("the commerce component isn't running — install it with `zc install commerce`")
	}
	defer conn.Close()

	req, _ := json.Marshal(struct {
		Text  string `json:"text"`
		Actor string `json:"actor"`
	}{Text: strings.TrimSpace(text), Actor: actor})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return "", false, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	line, readErr := bufio.NewReader(conn).ReadBytes('\n')
	if readErr != nil && len(line) == 0 {
		return "", false, readErr
	}
	var resp struct {
		OK       bool   `json:"ok"`
		Reply    string `json:"reply"`
		Continue bool   `json:"continue"`
		Error    string `json:"error"`
	}
	if json.Unmarshal(line, &resp) != nil {
		return "", false, fmt.Errorf("couldn't reach the commerce component")
	}
	if !resp.OK {
		return "", false, errors.New(resp.Error)
	}
	return resp.Reply, resp.Continue, nil
}
