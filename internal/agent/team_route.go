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

func isTeamCommand(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	switch lower {
	case "add task", "add tasks", "new task", "finish task", "team":
		return true
	}
	for _, prefix := range []string{"team ", "delegator ", "standup ", "staff ", "task ", "agent create "} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func (d *daemon) shouldRouteTeam(userID int64, text string) bool {
	if !d.teamInstalled {
		return false
	}
	d.mu.Lock()
	inSession := d.teamSessions[userID]
	d.mu.Unlock()
	return inSession || isTeamCommand(text)
}

func (d *daemon) routeToTeam(msg tdlib.Message, text string) {
	actor := d.telegramActor(msg.SenderID.UserID)
	reply, cont, err := d.teamCommand(text, actor)
	if err != nil {
		d.setTeamSession(msg.SenderID.UserID, false)
		d.send(msg.ChatID, "⚠️ "+err.Error())
		return
	}
	d.setTeamSession(msg.SenderID.UserID, cont)
	if reply != "" {
		d.send(msg.ChatID, reply)
	}
	if err := tdlib.MarkMessagesRead(d.tdjson, d.clientID, msg.ChatID, []int64{msg.ID}); err != nil {
		fmt.Printf("[team] couldn't mark message read: %v\n", err)
	}
}

func (d *daemon) setTeamSession(userID int64, on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if on {
		d.teamSessions[userID] = true
	} else {
		delete(d.teamSessions, userID)
	}
}

func (d *daemon) telegramActor(userID int64) string {
	if user, err := tdlib.FetchUser(d.tdjson, d.clientID, userID); err == nil {
		if username := strings.TrimSpace(user.Username); username != "" {
			return "@" + strings.TrimPrefix(username, "@")
		}
	}
	return fmt.Sprintf("user:%d", userID)
}

func (d *daemon) teamCommand(text, actor string) (reply string, cont bool, err error) {
	dir, err := configDir()
	if err != nil {
		return "", false, err
	}
	conn, err := net.DialTimeout("unix", filepath.Join(dir, "team.sock"), 2*time.Second)
	if err != nil {
		return "", false, fmt.Errorf("the team component isn't running — install it with `zc install team`")
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
		return "", false, fmt.Errorf("couldn't reach the team component")
	}
	if !resp.OK {
		return "", false, errors.New(resp.Error)
	}
	return resp.Reply, resp.Continue, nil
}
