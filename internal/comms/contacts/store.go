// Package contacts is the comms-owned contacts directory (comms.db): people and
// their per-platform handles. It belongs in comms because it is *addressing* —
// every tier above resolves "message <name> on whatever channel" through it via
// comms/client. The store is the single place both callers (the owner CLI and
// the running agent) funnel through, so all validation lives here.
package contacts

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/client"
	_ "modernc.org/sqlite"
)

// Caller identifies who is performing a write. Both the owner and the agent may
// write the contacts directory (it is addressing data, not a crown jewel), so
// the guard here always allows — but every write still takes a Caller so the
// CRUD shape matches the agent-tier stores, where it does gate.
type Caller string

const (
	Owner Caller = "owner"
	Agent Caller = "agent"
)

// CallerFrom maps a wire caller string to a Caller (default owner for local CLI).
func CallerFrom(s string) Caller {
	if Caller(s) == Agent {
		return Agent
	}
	return Owner
}

// Store is the SQLite-backed contacts directory.
type Store struct {
	db *sql.DB
}

// Open opens (creating + migrating) the contacts store at the given path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS contacts (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  note TEXT,
  created_at TEXT,
  updated_at TEXT
);
CREATE TABLE IF NOT EXISTS contact_handles (
  id INTEGER PRIMARY KEY,
  contact_id INTEGER NOT NULL REFERENCES contacts(id) ON DELETE CASCADE,
  platform TEXT NOT NULL,
  handle   TEXT NOT NULL,
  is_primary INTEGER NOT NULL DEFAULT 0,
  UNIQUE(platform, handle)
);`)
	return err
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// --- validation (lives in the store; both callers pass through here) ---------

var knownPlatforms = map[string]bool{
	"telegram": true, "whatsapp": true, "discord": true, "viber": true,
}

func validateHandle(h client.ContactHandle) error {
	if strings.TrimSpace(h.Platform) == "" || strings.TrimSpace(h.Handle) == "" {
		return fmt.Errorf("handle needs a platform and a handle")
	}
	if !knownPlatforms[strings.ToLower(h.Platform)] {
		return fmt.Errorf("unknown platform %q (want telegram|whatsapp|discord|viber)", h.Platform)
	}
	return nil
}

// --- contacts CRUD -----------------------------------------------------------

// List returns every contact with its handles, ordered by name.
func (s *Store) List() ([]client.Contact, error) {
	rows, err := s.db.Query(`SELECT id, name, COALESCE(note,'') FROM contacts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []client.Contact
	for rows.Next() {
		var c client.Contact
		if err := rows.Scan(&c.ID, &c.Name, &c.Note); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		h, err := s.handlesFor(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Handles = h
	}
	return out, nil
}

// Get returns one contact (with handles) by id.
func (s *Store) Get(id int64) (client.Contact, error) {
	var c client.Contact
	err := s.db.QueryRow(`SELECT id, name, COALESCE(note,'') FROM contacts WHERE id=?`, id).
		Scan(&c.ID, &c.Name, &c.Note)
	if err != nil {
		return c, err
	}
	c.Handles, err = s.handlesFor(id)
	return c, err
}

// Resolve returns contacts whose name matches (case-insensitive, exact then
// prefix), each with its handles — so callers can address a person by name.
func (s *Store) Resolve(name string) ([]client.Contact, error) {
	name = strings.TrimSpace(name)
	rows, err := s.db.Query(
		`SELECT id, name, COALESCE(note,'') FROM contacts
		 WHERE name=? COLLATE NOCASE OR name LIKE ? COLLATE NOCASE ORDER BY name`,
		name, name+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []client.Contact
	for rows.Next() {
		var c client.Contact
		if err := rows.Scan(&c.ID, &c.Name, &c.Note); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		h, err := s.handlesFor(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Handles = h
	}
	return out, nil
}

func (s *Store) handlesFor(contactID int64) ([]client.ContactHandle, error) {
	rows, err := s.db.Query(
		`SELECT id, contact_id, platform, handle, is_primary FROM contact_handles WHERE contact_id=? ORDER BY is_primary DESC, platform`,
		contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []client.ContactHandle
	for rows.Next() {
		var h client.ContactHandle
		var prim int
		if err := rows.Scan(&h.ID, &h.ContactID, &h.Platform, &h.Handle, &prim); err != nil {
			return nil, err
		}
		h.IsPrimary = prim == 1
		out = append(out, h)
	}
	return out, rows.Err()
}

// Create inserts a contact (and any handles it carries), returning it with ids.
func (s *Store) Create(_ Caller, c client.Contact) (client.Contact, error) {
	if strings.TrimSpace(c.Name) == "" {
		return c, fmt.Errorf("a contact needs a name")
	}
	for _, h := range c.Handles {
		if err := validateHandle(h); err != nil {
			return c, err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO contacts(name, note, created_at, updated_at) VALUES(?,?,?,?)`,
		c.Name, c.Note, now(), now())
	if err != nil {
		return c, err
	}
	c.ID, _ = res.LastInsertId()
	for i, h := range c.Handles {
		hres, err := tx.Exec(`INSERT INTO contact_handles(contact_id, platform, handle, is_primary) VALUES(?,?,?,?)`,
			c.ID, strings.ToLower(h.Platform), h.Handle, boolToInt(h.IsPrimary))
		if err != nil {
			return c, handleErr(err)
		}
		c.Handles[i].ID, _ = hres.LastInsertId()
		c.Handles[i].ContactID = c.ID
	}
	return c, tx.Commit()
}

// Update changes a contact's name/note (the only updatable columns).
func (s *Store) Update(_ Caller, id int64, name, note string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("a contact needs a name")
	}
	res, err := s.db.Exec(`UPDATE contacts SET name=?, note=?, updated_at=? WHERE id=?`, name, note, now(), id)
	if err != nil {
		return err
	}
	return mustAffect(res, id)
}

// Delete removes a contact and (via FK cascade) its handles.
func (s *Store) Delete(_ Caller, id int64) error {
	res, err := s.db.Exec(`DELETE FROM contacts WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res, id)
}

// AddHandle attaches a handle to a contact, enforcing unique (platform, handle).
func (s *Store) AddHandle(_ Caller, contactID int64, h client.ContactHandle) (client.ContactHandle, error) {
	if err := validateHandle(h); err != nil {
		return h, err
	}
	res, err := s.db.Exec(`INSERT INTO contact_handles(contact_id, platform, handle, is_primary) VALUES(?,?,?,?)`,
		contactID, strings.ToLower(h.Platform), h.Handle, boolToInt(h.IsPrimary))
	if err != nil {
		return h, handleErr(err)
	}
	h.ID, _ = res.LastInsertId()
	h.ContactID = contactID
	return h, nil
}

// RemoveHandle deletes a handle by (platform, handle).
func (s *Store) RemoveHandle(_ Caller, platform, handle string) error {
	res, err := s.db.Exec(`DELETE FROM contact_handles WHERE platform=? AND handle=?`, strings.ToLower(platform), handle)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no %s handle %q", platform, handle)
	}
	return nil
}

// Upsert inserts or updates a set of contacts by name (bulk path for the agent
// and importers, which write whole people with several handles at once).
func (s *Store) Upsert(caller Caller, cs []client.Contact) error {
	for _, c := range cs {
		existing, err := s.Resolve(c.Name)
		if err != nil {
			return err
		}
		var id int64
		exact := -1
		for i, e := range existing {
			if strings.EqualFold(e.Name, c.Name) {
				exact = i
				break
			}
		}
		if exact >= 0 {
			id = existing[exact].ID
			if err := s.Update(caller, id, c.Name, c.Note); err != nil {
				return err
			}
		} else {
			created, err := s.Create(caller, client.Contact{Name: c.Name, Note: c.Note})
			if err != nil {
				return err
			}
			id = created.ID
		}
		for _, h := range c.Handles {
			// Skip a handle that already exists (unique constraint) — upsert is
			// idempotent on re-import.
			if _, err := s.AddHandle(caller, id, h); err != nil && !strings.Contains(err.Error(), "already") {
				return err
			}
		}
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func handleErr(err error) error {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return fmt.Errorf("that handle is already registered to a contact (platform+handle must be unique)")
	}
	return err
}

func mustAffect(res sql.Result, id int64) error {
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no contact with id %d", id)
	}
	return nil
}
