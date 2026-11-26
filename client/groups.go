package client

import "time"

// SendOpts sends a message on a transport, optionally quoting replyTo (a
// message id in that conversation) and tagging mentions (member platform ids).
// to may be a contact, a group name, a negative Telegram chat id, an
// @groupusername or a WhatsApp @g.us JID. The sent message's id comes back in
// Response.MsgRef (the final Telegram id, not TDLib's temporary one), ready to
// pass as replyTo later.
func (c *Client) SendOpts(transport, to, text, replyTo string, mentions []string) (Response, error) {
	return c.Do(Request{Op: "send", Transport: transport, To: to, Text: text, ReplyTo: replyTo, Mentions: mentions}, time.Now().Add(sendDeadline))
}

// GroupCreate creates a group on a transport with the given members (names or
// addresses, resolved like a send target). The daemon always adds the owner as
// well. When adding the owner fails the group still exists: the returned Group
// is set and err carries the reason.
func (c *Client) GroupCreate(transport, name string, members []string) (*Group, error) {
	resp, err := c.Do(Request{Op: "group_create", Transport: transport, Name: name, Members: members}, time.Now().Add(90*time.Second))
	return resp.Group, err
}

// GroupAdd adds members to an existing group.
func (c *Client) GroupAdd(transport, group string, members []string) error {
	_, err := c.Do(Request{Op: "group_add", Transport: transport, To: group, Members: members}, time.Now().Add(90*time.Second))
	return err
}

// GroupList lists the groups the account is in on a transport and syncs them
// into the comms.db registry.
func (c *Client) GroupList(transport string) ([]Group, error) {
	resp, err := c.Do(Request{Op: "group_list", Transport: transport}, time.Now().Add(90*time.Second))
	return resp.Groups, err
}

// GroupMembers lists a group's members.
func (c *Client) GroupMembers(transport, group string) ([]GroupMember, error) {
	resp, err := c.Do(Request{Op: "group_members", Transport: transport, To: group}, time.Now().Add(60*time.Second))
	return resp.Members, err
}

// GroupReadOpts selects a group_read window. Zero values mean: the latest
// messages, default limit.
type GroupReadOpts struct {
	Limit  int
	Around string
	Before string
	Since  int64
}

// GroupRead reads a window of a group's messages in time order. An agent
// caller is limited to listen groups, around a recent call, or since a time
// for follow groups.
func (c *Client) GroupRead(transport, group string, o GroupReadOpts) ([]GroupMessage, error) {
	resp, err := c.Do(Request{
		Op: "group_read", Transport: transport, To: group,
		Limit: o.Limit, Around: o.Around, Before: o.Before, Since: o.Since,
	}, time.Now().Add(90*time.Second))
	return resp.GroupMessages, err
}

// GroupSet changes a group's listen and/or follow flags (nil leaves a flag as
// it is). Refused for an agent caller.
func (c *Client) GroupSet(transport, group string, listen, follow *bool) (*Group, error) {
	resp, err := c.Do(Request{Op: "group_set", Transport: transport, To: group, Listen: listen, Follow: follow}, time.Now().Add(60*time.Second))
	return resp.Group, err
}

// GroupGet resolves a group name, alias or native id to its registry row.
func (c *Client) GroupGet(transport, group string) (*Group, error) {
	resp, err := c.Do(Request{Op: "group_get", Transport: transport, To: group}, time.Now().Add(60*time.Second))
	return resp.Group, err
}

// SendOptions are the extras of a send: the message id to quote, member ids
// to tag, and the thread to post in (a topic id or name; WhatsApp has none,
// and the send then goes to the group with Response.ThreadIgnored set).
type SendOptions struct {
	ReplyTo  string
	Mentions []string
	Thread   string
}

// SendWith sends text with quote, tags and thread.
func (c *Client) SendWith(transport, to, text string, o SendOptions) (Response, error) {
	return c.Do(Request{Op: "send", Transport: transport, To: to, Text: text,
		ReplyTo: o.ReplyTo, Mentions: o.Mentions, Thread: o.Thread}, time.Now().Add(sendDeadline))
}

// SendFileWith sends a local file (photos as photos, other files as
// documents, at most 20 MB) quoting o.ReplyTo and posting in o.Thread.
func (c *Client) SendFileWith(transport, to, path, caption string, o SendOptions) (Response, error) {
	return c.Do(Request{Op: "sendfile", Transport: transport, To: to, Path: path, Text: caption,
		ReplyTo: o.ReplyTo, Thread: o.Thread}, time.Now().Add(31*time.Minute))
}

// GroupCreateTopics is GroupCreate with threads on from the start (Telegram
// forum supergroup).
func (c *Client) GroupCreateTopics(transport, name string, members []string) (*Group, error) {
	resp, err := c.Do(Request{Op: "group_create", Transport: transport, Name: name, Members: members, Topics: true}, time.Now().Add(90*time.Second))
	return resp.Group, err
}

// TopicCreate creates a thread in a group and returns it. A Telegram
// supergroup the account created gets topics turned on first; otherwise a
// group without topics is an error. WhatsApp answers that its groups have
// no threads.
func (c *Client) TopicCreate(transport, group, name string) (*Topic, error) {
	resp, err := c.Do(Request{Op: "topic_create", Transport: transport, To: group, Name: name}, time.Now().Add(60*time.Second))
	return resp.Topic, err
}

// TopicList lists a group's threads.
func (c *Client) TopicList(transport, group string) ([]Topic, error) {
	resp, err := c.Do(Request{Op: "topic_list", Transport: transport, To: group}, time.Now().Add(60*time.Second))
	return resp.Topics, err
}
