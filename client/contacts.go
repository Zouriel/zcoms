package client

import "time"

// The contacts directory (comms.db) reached over IPC. Every tier addresses
// people by name on any platform through these — they never open comms.db.

func (c *Client) contactDo(req Request) ([]Contact, error) {
	resp, err := c.Do(req, time.Now().Add(15*time.Second))
	return resp.Contacts, err
}

// ResolveContact returns contacts whose name matches, each with its handles.
func (c *Client) ResolveContact(name string) ([]Contact, error) {
	return c.contactDo(Request{Op: "contact_resolve", To: name})
}

// ListContacts returns the whole contacts directory.
func (c *Client) ListContacts() ([]Contact, error) {
	return c.contactDo(Request{Op: "contact_list"})
}

// CreateContact adds a contact (with any handles it carries).
func (c *Client) CreateContact(contact Contact) (Contact, error) {
	cs, err := c.contactDo(Request{Op: "contact_create", Contact: &contact})
	if err != nil || len(cs) == 0 {
		return Contact{}, err
	}
	return cs[0], nil
}

// UpdateContact changes a contact's name/note.
func (c *Client) UpdateContact(id int64, name, note string) error {
	_, err := c.contactDo(Request{Op: "contact_update", Contact: &Contact{ID: id, Name: name, Note: note}})
	return err
}

// DeleteContact removes a contact and its handles.
func (c *Client) DeleteContact(id int64) error {
	_, err := c.contactDo(Request{Op: "contact_delete", Contact: &Contact{ID: id}})
	return err
}

// UpsertContact inserts or updates a contact by name (bulk-friendly path).
func (c *Client) UpsertContact(contact Contact) error {
	_, err := c.contactDo(Request{Op: "contact_upsert", Contact: &contact})
	return err
}

// AddHandle attaches a platform handle to a contact.
func (c *Client) AddHandle(contactID int64, platform, handle string) error {
	_, err := c.contactDo(Request{Op: "contact_handle_add", Contact: &Contact{ID: contactID}, Platform: platform, Handle: handle})
	return err
}

// RemoveHandle removes a handle by (platform, handle).
func (c *Client) RemoveHandle(platform, handle string) error {
	_, err := c.contactDo(Request{Op: "contact_handle_remove", Platform: platform, Handle: handle})
	return err
}
