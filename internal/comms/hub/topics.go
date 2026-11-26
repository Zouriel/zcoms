package hub

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/telegram"
	"github.com/Zouriel/zcoms/internal/comms/transport"
)

var (
	_ transport.Topicer    = (*telegramTransport)(nil)
	_ transport.FileSender = (*telegramTransport)(nil)
)

// MaxSendFileBytes caps a file sent through the sendfile op (20 MB).
const MaxSendFileBytes = 20 << 20

// noThreadsError is what a topic op or thread request answers on a transport
// whose groups have no threads.
func noThreadsError(transportName string) error {
	switch transportName {
	case "whatsapp":
		return errors.New("WhatsApp groups do not have threads")
	}
	return fmt.Errorf("%s groups do not have threads", transportName)
}

// topicer returns a transport's thread capability.
func (d *daemon) topicer(transportName string) (transport.Topicer, error) {
	t, ok := d.registry[transportName]
	if !ok {
		return nil, fmt.Errorf("unknown transport: %s", transportName)
	}
	tp, ok := t.(transport.Topicer)
	if !ok {
		return nil, noThreadsError(transportName)
	}
	return tp, nil
}

// supportsThreads reports whether a transport's groups can hold threads.
func (d *daemon) supportsThreads(transportName string) bool {
	_, ok := d.registry[transportName].(transport.Topicer)
	return ok
}

// topicOp serves topic_create and topic_list. The agent may use them only in
// a group that is on (listen), which is the only kind it is ever called in.
func (d *daemon) topicOp(req client.Request, agent bool) (client.Response, error) {
	tn := transportName(req)
	tp, err := d.topicer(tn)
	if err != nil {
		return client.Response{}, err
	}
	grp, err := d.resolveGroupRef(tn, req.To)
	if err != nil {
		return client.Response{}, err
	}
	if agent && !grp.Listen {
		return client.Response{}, errors.New("the agent may use threads only in a group that is on")
	}
	switch req.Op {
	case "topic_create":
		name := strings.TrimSpace(req.Name)
		if name == "" {
			return client.Response{}, errors.New("a thread needs a name")
		}
		if len([]rune(name)) > 128 {
			name = string([]rune(name)[:128])
		}
		t, err := tp.CreateTopic(grp.NativeID, name)
		if err != nil {
			return client.Response{Group: &grp}, err
		}
		return client.Response{Group: &grp, Topic: &client.Topic{ID: t.ID, Name: t.Name}}, nil
	case "topic_rename":
		// Owner only: the agent never renames threads.
		if agent {
			return client.Response{}, errors.New("only the owner can rename a thread")
		}
		name := strings.TrimSpace(req.Name)
		if name == "" || strings.TrimSpace(req.Thread) == "" {
			return client.Response{}, errors.New("topic_rename needs the thread (id or name) and a new name")
		}
		ts, err := tp.Topics(grp.NativeID)
		if err != nil {
			return client.Response{Group: &grp}, err
		}
		var hit *transport.Topic
		for i, t := range ts {
			if t.ID == req.Thread || strings.EqualFold(strings.TrimSpace(t.Name), strings.TrimSpace(req.Thread)) {
				hit = &ts[i]
				break
			}
		}
		if hit == nil {
			return client.Response{Group: &grp}, fmt.Errorf("no thread %q in %s", req.Thread, grp.Name)
		}
		if err := tp.RenameTopic(grp.NativeID, hit.ID, name); err != nil {
			return client.Response{Group: &grp}, err
		}
		return client.Response{Group: &grp, Topic: &client.Topic{ID: hit.ID, Name: name}}, nil
	case "topic_list":
		ts, err := tp.Topics(grp.NativeID)
		if err != nil {
			return client.Response{Group: &grp}, err
		}
		out := make([]client.Topic, 0, len(ts))
		for _, t := range ts {
			out = append(out, client.Topic{ID: t.ID, Name: t.Name})
		}
		return client.Response{Group: &grp, Topics: out}, nil
	}
	return client.Response{}, fmt.Errorf("unknown op: %s", req.Op)
}

// checkSendFile refuses what the sendfile op must not upload: a missing path,
// a directory, or anything over MaxSendFileBytes.
func checkSendFile(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("sendfile needs a path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file not found: %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > MaxSendFileBytes {
		return fmt.Errorf("%s is %.1f MB; files are limited to 20 MB", path, float64(info.Size())/(1<<20))
	}
	return nil
}

// CreateTopic creates a forum topic in a group. A supergroup the account
// created gets topics turned on first; a basic group is never upgraded (its
// chat id would change), and a group someone else owns answers that an admin
// has to turn topics on.
func (t *telegramTransport) CreateTopic(group, name string) (transport.Topic, error) {
	chatID, err := groupChatID(group)
	if err != nil {
		return transport.Topic{}, err
	}
	me, _ := t.d.self()
	if err := telegram.EnsureForum(t.d.tdjson, t.d.clientID, chatID, me); err != nil {
		return transport.Topic{}, err
	}
	ft, err := telegram.CreateForumTopic(t.d.tdjson, t.d.clientID, chatID, name)
	if err != nil {
		return transport.Topic{}, err
	}
	if ft.Name == "" {
		ft.Name = name
	}
	return transport.Topic{ID: strconv.FormatInt(ft.ID, 10), Name: ft.Name}, nil
}

// RenameTopic renames one of a group's forum topics.
func (t *telegramTransport) RenameTopic(group, topicID, name string) error {
	chatID, err := groupChatID(group)
	if err != nil {
		return err
	}
	id, err := strconv.ParseInt(topicID, 10, 64)
	if err != nil {
		return fmt.Errorf("bad thread id %q", topicID)
	}
	return telegram.RenameForumTopic(t.d.tdjson, t.d.clientID, chatID, id, name)
}

// Topics lists a group's forum topics.
func (t *telegramTransport) Topics(group string) ([]transport.Topic, error) {
	chatID, err := groupChatID(group)
	if err != nil {
		return nil, err
	}
	fts, err := telegram.ForumTopics(t.d.tdjson, t.d.clientID, chatID)
	if err != nil {
		return nil, err
	}
	out := make([]transport.Topic, 0, len(fts))
	for _, ft := range fts {
		out = append(out, transport.Topic{ID: strconv.FormatInt(ft.ID, 10), Name: ft.Name})
	}
	return out, nil
}

// threadID turns a thread given as a topic id or a topic name into the
// forum topic id. Empty is 0 (no thread).
func (d *daemon) threadID(chatID int64, thread string) (int64, error) {
	thread = strings.TrimSpace(thread)
	if thread == "" {
		return 0, nil
	}
	if id, err := strconv.ParseInt(thread, 10, 64); err == nil {
		if id <= 0 {
			return 0, fmt.Errorf("bad thread id %q", thread)
		}
		return id, nil
	}
	if d.tdjson == nil {
		return 0, fmt.Errorf("no thread named %q", thread)
	}
	fts, err := telegram.ForumTopics(d.tdjson, d.clientID, chatID)
	if err != nil {
		return 0, err
	}
	return pickTopic(fts, thread)
}

// pickTopic finds a topic by name: an exact (case-insensitive) match, else
// the one topic whose name starts with it.
func pickTopic(fts []telegram.ForumTopic, name string) (int64, error) {
	var prefix []telegram.ForumTopic
	for _, ft := range fts {
		if strings.EqualFold(ft.Name, name) {
			return ft.ID, nil
		}
		if strings.HasPrefix(strings.ToLower(ft.Name), strings.ToLower(name)) {
			prefix = append(prefix, ft)
		}
	}
	switch len(prefix) {
	case 1:
		return prefix[0].ID, nil
	case 0:
		return 0, fmt.Errorf("no thread named %q in this group", name)
	}
	names := make([]string, 0, len(prefix))
	for _, ft := range prefix {
		names = append(names, ft.Name)
	}
	return 0, fmt.Errorf("%q matches several threads (%s): say which one", name, strings.Join(names, ", "))
}

// SendFileWith uploads a local file quoting o.ReplyTo and posting in
// o.Thread. Like SendFile it returns once the send is accepted.
func (t *telegramTransport) SendFileWith(to transport.Address, path, caption string, o transport.SendOptions) (transport.MsgRef, error) {
	chatID, _, err := t.d.resolveChat(to.ID)
	if err != nil {
		return transport.MsgRef{}, err
	}
	var fo telegram.FileOptions
	if fo.Thread, err = t.d.threadID(chatID, o.Thread); err != nil {
		return transport.MsgRef{}, err
	}
	if o.ReplyTo != "" {
		if fo.ReplyTo, err = strconv.ParseInt(o.ReplyTo, 10, 64); err != nil {
			return transport.MsgRef{}, fmt.Errorf("bad reply_to message id %q", o.ReplyTo)
		}
	}
	_, label, err := telegram.SendLocalFileMessageWith(t.d.tdjson, t.d.clientID, chatID, path, caption, fo)
	if err != nil {
		return transport.MsgRef{}, err
	}
	return transport.MsgRef{ChatID: strconv.FormatInt(chatID, 10), Label: label}, nil
}
