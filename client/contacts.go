package client

import "time"

// The contacts directory (comms.db) reached over IPC. Every tier addresses
// people by name on any channel through these — they never open comms.db.

func (c *Client) contactDo(req Request) ([]Contact, error) {
	resp, err := c.Do(req, time.Now().Add(15*time.Second))
	return resp.Contacts, err
}

// ResolveContact returns contacts whose name matches, each with its addresses.
func (c *Client) ResolveContact(name string) ([]Contact, error) {
	return c.contactDo(Request{Op: "contact_resolve", To: name})
}

// ListContacts returns the whole contacts directory.
func (c *Client) ListContacts() ([]Contact, error) {
	return c.contactDo(Request{Op: "contact_list"})
}

// CreateContact adds a contact (all channel fields it carries).
func (c *Client) CreateContact(contact Contact) (Contact, error) {
	cs, err := c.contactDo(Request{Op: "contact_create", Contact: &contact})
	if err != nil || len(cs) == 0 {
		return Contact{}, err
	}
	return cs[0], nil
}

// UpdateContact overwrites every channel field of a contact (addressed by its
// ID), so pass a fully-populated Contact.
func (c *Client) UpdateContact(contact Contact) error {
	_, err := c.contactDo(Request{Op: "contact_update", Contact: &contact})
	return err
}

// DeleteContact removes a contact.
func (c *Client) DeleteContact(id int64) error {
	_, err := c.contactDo(Request{Op: "contact_delete", Contact: &Contact{ID: id}})
	return err
}

// UpsertContact inserts or updates a contact by name (bulk-friendly path).
func (c *Client) UpsertContact(contact Contact) error {
	_, err := c.contactDo(Request{Op: "contact_upsert", Contact: &contact})
	return err
}
