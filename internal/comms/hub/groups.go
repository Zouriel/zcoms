package hub

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/comms/transport"
	"github.com/Zouriel/zcoms/internal/config"
)

// Agent-caller limits on group_read (spec 1d, safety rule 6).
const (
	agentWindowMax  = 50             // messages each side of a call
	callWindow      = 24 * time.Hour // a call is readable around for this long
	ownerReadLimit  = 50             // default window for the owner
	maxReadMessages = 2000           // hard ceiling on any one read
)

// groupFlags reads a group's registry flags. An unknown group is off.
func (d *daemon) groupFlags(transportName, nativeID string) transport.GroupFlags {
	if d.contacts == nil {
		return transport.GroupFlags{}
	}
	g, err := d.contacts.GroupByNative(transportName, nativeID)
	if err != nil {
		return transport.GroupFlags{}
	}
	return transport.GroupFlags{Name: g.Name, Listen: g.Listen, Follow: g.Follow}
}

// groupForName decides whether a bare name means a group on a transport. The
// contacts directory is consulted too, so a name matching both a person and a
// group is refused rather than guessed: sending to the wrong one cannot be
// taken back. ok=false with no error means "not a group, try contacts".
func (d *daemon) groupForName(transportName, name string) (client.Group, bool, error) {
	if d.contacts == nil {
		return client.Group{}, false, nil
	}
	exact, prefix, err := d.contacts.ResolveGroups(transportName, name)
	if err != nil || len(exact)+len(prefix) == 0 {
		return client.Group{}, false, nil
	}
	people, _ := d.contacts.Resolve(name)
	contactExact := false
	for _, c := range people {
		for _, cand := range append([]string{c.Name}, c.Aliases...) {
			if strings.EqualFold(strings.TrimSpace(cand), strings.TrimSpace(name)) {
				contactExact = true
			}
		}
	}

	groups := exact
	if len(groups) == 0 {
		if contactExact {
			return client.Group{}, false, nil // a person by that exact name wins over a partial group name
		}
		groups = prefix
	}
	if len(groups) > 1 {
		return client.Group{}, false, fmt.Errorf("%q matches several groups (%s): say which one", name, groupNames(groups))
	}
	if contactExact || (len(exact) == 0 && len(people) > 0) {
		return client.Group{}, false, fmt.Errorf("%q matches a contact and the group %q: say which one", name, groups[0].Name)
	}
	return groups[0], true, nil
}

func groupNames(gs []client.Group) string {
	names := make([]string, 0, len(gs))
	for _, g := range gs {
		names = append(names, g.Name)
	}
	return strings.Join(names, ", ")
}

// grouper returns a transport's group capability.
func (d *daemon) grouper(transportName string) (transport.Grouper, error) {
	t, ok := d.registry[transportName]
	if !ok {
		return nil, fmt.Errorf("unknown transport: %s", transportName)
	}
	g, ok := t.(transport.Grouper)
	if !ok || !t.Caps().Groups {
		return nil, fmt.Errorf("%s has no group support", transportName)
	}
	return g, nil
}

// nativeGroupID recognises an address that is already a group's native id.
func (d *daemon) nativeGroupID(transportName, to string) (string, bool, error) {
	switch transportName {
	case "telegram":
		if id, ok := chatIDOf(to); ok {
			if id >= 0 {
				return "", false, fmt.Errorf("%s is a private chat, not a group", to)
			}
			return to, true, nil
		}
		if strings.HasPrefix(to, "@") && d.tdjson != nil {
			id, err := telegram.ResolveChatIdentifierByUsername(d.tdjson, d.clientID, to)
			if err != nil {
				return "", false, err
			}
			if id >= 0 {
				return "", false, fmt.Errorf("%s is a person, not a group", to)
			}
			return strconv.FormatInt(id, 10), true, nil
		}
	case "whatsapp":
		if strings.HasSuffix(to, "@g.us") {
			return to, true, nil
		}
	}
	return "", false, nil
}

// resolveGroupRef turns a group name, alias or native id into its registry
// row, registering a group seen for the first time. An unknown name triggers
// one refresh of the transport's group list before giving up.
func (d *daemon) resolveGroupRef(transportName, to string) (client.Group, error) {
	to = strings.TrimSpace(to)
	if to == "" {
		return client.Group{}, fmt.Errorf("say which group")
	}
	if d.contacts == nil {
		return client.Group{}, fmt.Errorf("the group registry is unavailable")
	}
	native, ok, err := d.nativeGroupID(transportName, to)
	if err != nil {
		return client.Group{}, err
	}
	if ok {
		g, err := d.contacts.GroupByNative(transportName, native)
		if err == nil {
			return g, nil
		}
		if !errors.Is(err, contacts.ErrNoGroup) {
			return client.Group{}, err
		}
		name := native
		if infos, err := d.listGroups(transportName); err == nil {
			for _, gi := range infos {
				if gi.NativeID == native {
					name = gi.Name
				}
			}
		}
		return d.contacts.UpsertGroup(transportName, native, name)
	}

	for attempt := 0; attempt < 2; attempt++ {
		exact, prefix, err := d.contacts.ResolveGroups(transportName, to)
		if err != nil {
			return client.Group{}, err
		}
		groups := exact
		if len(groups) == 0 {
			groups = prefix
		}
		if len(groups) == 1 {
			return groups[0], nil
		}
		if len(groups) > 1 {
			return client.Group{}, fmt.Errorf("%q matches several groups (%s): say which one", to, groupNames(groups))
		}
		if attempt == 0 {
			if _, err := d.syncGroups(transportName); err != nil {
				return client.Group{}, err
			}
		}
	}
	return client.Group{}, fmt.Errorf("no %s group named %q", transportName, to)
}

func (d *daemon) listGroups(transportName string) ([]transport.GroupInfo, error) {
	g, err := d.grouper(transportName)
	if err != nil {
		return nil, err
	}
	return g.Groups()
}

// syncGroups lists the transport's groups and upserts them into the registry.
func (d *daemon) syncGroups(transportName string) ([]client.Group, error) {
	infos, err := d.listGroups(transportName)
	if err != nil {
		return nil, err
	}
	out := make([]client.Group, 0, len(infos))
	for _, gi := range infos {
		g, err := d.contacts.UpsertGroup(transportName, gi.NativeID, gi.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// memberAddress resolves a member (contact name, handle, number or id) to the
// native user address the transport's group calls take.
func (d *daemon) memberAddress(transportName, member string) (string, error) {
	switch transportName {
	case "telegram":
		chatID, uid, err := d.resolveChat(member)
		if err != nil {
			return "", fmt.Errorf("%s: %w", member, err)
		}
		if uid == 0 || chatID < 0 {
			return "", fmt.Errorf("%s is a group, not a person", member)
		}
		return strconv.FormatInt(uid, 10), nil
	case "whatsapp":
		jid, err := d.resolveWhatsApp(member)
		if err != nil {
			return "", err
		}
		if strings.HasSuffix(jid, "@g.us") {
			return "", fmt.Errorf("%s is a group, not a person", member)
		}
		return jid, nil
	}
	return "", fmt.Errorf("unknown transport: %s", transportName)
}

// ownerAddress resolves config.json's owner on a transport.
func (d *daemon) ownerAddress(transportName string) (string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", fmt.Errorf("reading config: %w", err)
	}
	owner := strings.TrimSpace(cfg.Owner)
	if owner == "" {
		return "", errors.New("group created but the owner is not set: run `zc config owner <contact>`")
	}
	addr, err := d.memberAddress(transportName, owner)
	if err != nil {
		return "", fmt.Errorf("group created but the owner %q could not be resolved: %w", owner, err)
	}
	return addr, nil
}

func (d *daemon) isSelfAddress(transportName, addr string) bool {
	if transportName != "telegram" {
		return false
	}
	me, _ := d.self()
	return me != 0 && addr == strconv.FormatInt(me, 10)
}

// handleGroupOp serves the group_* ops.
func (d *daemon) handleGroupOp(conn net.Conn, req client.Request) {
	resp, err := d.groupOp(req)
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
	} else {
		resp.OK = true
	}
	writeIPC(conn, resp)
}

func (d *daemon) groupOp(req client.Request) (client.Response, error) {
	tn := transportName(req)
	if d.contacts == nil {
		return client.Response{}, errors.New("the group registry is unavailable")
	}
	agent := contacts.CallerFrom(req.Caller) == contacts.Agent

	switch req.Op {
	case "group_create":
		return d.groupCreate(tn, req)

	case "group_add":
		g, err := d.grouper(tn)
		if err != nil {
			return client.Response{}, err
		}
		grp, err := d.resolveGroupRef(tn, req.To)
		if err != nil {
			return client.Response{}, err
		}
		addrs, err := d.memberAddresses(tn, req.Members)
		if err != nil {
			return client.Response{}, err
		}
		if len(addrs) == 0 {
			return client.Response{}, errors.New("say who to add")
		}
		return client.Response{Group: &grp}, g.AddGroupMembers(grp.NativeID, addrs)

	case "group_list":
		groups, err := d.syncGroups(tn)
		return client.Response{Groups: groups}, err

	case "group_members":
		g, err := d.grouper(tn)
		if err != nil {
			return client.Response{}, err
		}
		grp, err := d.resolveGroupRef(tn, req.To)
		if err != nil {
			return client.Response{}, err
		}
		ms, err := g.GroupMembers(grp.NativeID)
		if err != nil {
			return client.Response{}, err
		}
		out := make([]client.GroupMember, 0, len(ms))
		for _, m := range ms {
			out = append(out, client.GroupMember{ID: m.ID, Name: m.Name, Username: m.Username, IsSelf: m.IsSelf})
		}
		return client.Response{Group: &grp, Members: out}, nil

	case "group_read":
		return d.groupRead(tn, req, agent)

	case "group_set":
		if agent {
			return client.Response{}, errors.New("group_set is owner-only")
		}
		grp, err := d.resolveGroupRef(tn, req.To)
		if err != nil {
			return client.Response{}, err
		}
		grp, err = d.contacts.SetGroupFlags(contacts.Owner, tn, grp.NativeID, req.Listen, req.Follow)
		return client.Response{Group: &grp}, err

	case "group_get":
		grp, err := d.resolveGroupRef(tn, req.To)
		return client.Response{Group: &grp}, err
	}
	return client.Response{}, fmt.Errorf("unknown op: %s", req.Op)
}

func (d *daemon) memberAddresses(tn string, members []string) ([]string, error) {
	out := make([]string, 0, len(members))
	for _, m := range members {
		if strings.TrimSpace(m) == "" {
			continue
		}
		addr, err := d.memberAddress(tn, m)
		if err != nil {
			return nil, err
		}
		out = append(out, addr)
	}
	return out, nil
}

// groupCreate creates the group, then always adds the owner. A failure after
// the group exists still returns the group, with the reason in the error.
func (d *daemon) groupCreate(tn string, req client.Request) (client.Response, error) {
	g, err := d.grouper(tn)
	if err != nil {
		return client.Response{}, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return client.Response{}, errors.New("a group needs a name")
	}
	if tn == "whatsapp" && len([]rune(name)) > 25 {
		return client.Response{}, errors.New("whatsapp group names are limited to 25 characters")
	}
	addrs, err := d.memberAddresses(tn, req.Members)
	if err != nil {
		return client.Response{}, err
	}
	// Resolve the owner before creating, so the add afterwards cannot fail on
	// something that was knowable up front; the error is reported after create.
	ownerAddr, ownerErr := d.ownerAddress(tn)

	info, createErr := g.CreateGroup(name, addrs)
	if info.NativeID == "" {
		if createErr == nil {
			createErr = errors.New("the transport returned no group id")
		}
		return client.Response{}, createErr
	}
	grp, err := d.contacts.UpsertGroup(tn, info.NativeID, info.Name)
	if err != nil {
		return client.Response{}, err
	}

	var problems []string
	if createErr != nil {
		problems = append(problems, createErr.Error())
	}
	switch {
	case ownerErr != nil:
		problems = append(problems, ownerErr.Error())
	case d.isSelfAddress(tn, ownerAddr) || containsFold(addrs, ownerAddr):
		// already in the group
	default:
		if err := g.AddGroupMembers(info.NativeID, []string{ownerAddr}); err != nil {
			problems = append(problems, "group created but adding the owner failed: "+err.Error())
		}
	}
	if len(problems) > 0 {
		return client.Response{Group: &grp}, errors.New(strings.Join(problems, "; "))
	}
	return client.Response{Group: &grp}, nil
}

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// groupRead serves group_read. The owner may read any group the account is
// in. The agent may read only a listen group, only around a message that was
// forwarded to it as a call in the last day (50 each side at most), or by
// date for a follow group (the standup job).
func (d *daemon) groupRead(tn string, req client.Request, agent bool) (client.Response, error) {
	g, err := d.grouper(tn)
	if err != nil {
		return client.Response{}, err
	}
	grp, err := d.resolveGroupRef(tn, req.To)
	if err != nil {
		return client.Response{}, err
	}
	q := transport.GroupQuery{Limit: req.Limit, Around: strings.TrimSpace(req.Around), Before: strings.TrimSpace(req.Before)}
	if req.Since > 0 {
		q.Since = time.Unix(req.Since, 0)
	}
	if err := checkGroupRead(d.contacts, tn, grp, &q, agent); err != nil {
		return client.Response{Group: &grp}, err
	}
	msgs, err := g.GroupHistory(grp.NativeID, q)
	if err != nil {
		return client.Response{Group: &grp}, err
	}
	out := make([]client.GroupMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, client.GroupMessage{
			ID: m.ID, SenderID: m.SenderID, SenderName: m.SenderName, Text: m.Text, Kind: m.Kind,
			Date: m.At.Unix(), Outgoing: m.Outgoing, ReplyToID: m.ReplyToID,
		})
	}
	return client.Response{Group: &grp, GroupMessages: out}, nil
}

// callChecker is the part of the store checkGroupRead needs.
type callChecker interface {
	RecentCall(transportName, nativeID, msgID string, window time.Duration) (bool, error)
}

// checkGroupRead applies the caller rules to a read and normalises its limit.
func checkGroupRead(calls callChecker, tn string, grp client.Group, q *transport.GroupQuery, agent bool) error {
	if !agent {
		if q.Limit <= 0 {
			q.Limit = ownerReadLimit
		}
		if q.Limit > maxReadMessages {
			q.Limit = maxReadMessages
		}
		return nil
	}
	if !grp.Listen {
		return fmt.Errorf("group %q is off: the agent cannot read it", grp.Name)
	}
	switch {
	case !q.Since.IsZero():
		if !grp.Follow {
			return fmt.Errorf("group %q is not followed: reading by date is not allowed", grp.Name)
		}
		if q.Around != "" || q.Before != "" {
			return errors.New("give either since or around, not both")
		}
		if q.Limit <= 0 || q.Limit > maxReadMessages {
			q.Limit = maxReadMessages
		}
		return nil
	case q.Around != "":
		if q.Before != "" {
			return errors.New("give either before or around, not both")
		}
		ok, err := calls.RecentCall(tn, grp.NativeID, q.Around, callWindow)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("message %s is not a call from the last 24 hours", q.Around)
		}
		if q.Limit <= 0 || q.Limit > agentWindowMax {
			q.Limit = agentWindowMax
		}
		return nil
	}
	return errors.New("the agent may read a group only around a recent call, or by date for a followed group")
}

// groupEvent turns a group call into the Event subscribers receive, and
// records the call so the agent may read around it.
func (d *daemon) groupEvent(in transport.Inbound) client.Event {
	if d.contacts != nil {
		if err := d.contacts.RecordCall(in.From.Transport, in.From.ID, in.MessageID, time.Now()); err != nil {
			d.logf("recording a group call: %v", err)
		}
	}
	ev := client.Event{
		Event:     "message",
		Transport: in.From.Transport,
		Address:   in.From.ID,
		Sender:    in.Sender,
		Text:      in.Text,
		Kind:      in.Kind,
		MessageID: parseIntOrZero(in.MessageID),
		MsgRef:    in.MessageID,
		Date:      in.At.Unix(),
		Group:     true,
		GroupName: in.GroupName,
		SenderID:  in.SenderID,
		ReplyToID: in.ReplyToID,
		Call:      in.Call,
	}
	if in.From.Transport == "telegram" {
		ev.ChatID = parseIntOrZero(in.From.ID)
	}
	return ev
}
