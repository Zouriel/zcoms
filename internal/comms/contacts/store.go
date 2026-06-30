// Package contacts is the comms-owned contacts directory (comms.db): people and
// their per-channel addresses. It belongs in comms because it is *addressing* —
// every tier above resolves "message <name> on whatever channel" through it via
// comms/client. Fields are explicit per channel (phone, email, telegram,
// whatsapp, discord, viber) rather than a generic handle list: Phone is the
// universal number that reaches Telegram/WhatsApp/Viber, the per-platform ids
// override it, and Discord has no phone fallback. The store is the single place
// both callers (the owner CLI and the running agent) funnel through, so all
// validation lives here.
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
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS contacts (
  id INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  created_at TEXT,
  updated_at TEXT
);`); err != nil {
		return err
	}
	// Add the channel columns idempotently (SQLite has no ADD COLUMN IF NOT
	// EXISTS) — this also upgrades a legacy contacts table in place.
	for _, col := range []string{"phone", "email", "telegram", "whatsapp", "instagram", "discord", "viber", "note", "aliases", "github"} {
		if _, err := s.db.Exec(`ALTER TABLE contacts ADD COLUMN ` + col + ` TEXT`); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}
	// The old per-handle table is gone — addresses are explicit columns now.
	_, err := s.db.Exec(`DROP TABLE IF EXISTS contact_handles`)
	return err
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// selectCols is the column list every read scans, in scanContact order.
const selectCols = `id, name,
	COALESCE(phone,''), COALESCE(email,''), COALESCE(telegram,''),
	COALESCE(whatsapp,''), COALESCE(instagram,''), COALESCE(discord,''),
	COALESCE(viber,''), COALESCE(github,''), COALESCE(note,''), COALESCE(aliases,'')`

func scanContact(sc interface{ Scan(...any) error }) (client.Contact, error) {
	var c client.Contact
	var aliases string
	err := sc.Scan(&c.ID, &c.Name, &c.Phone, &c.Email, &c.Telegram, &c.WhatsApp,
		&c.Instagram, &c.Discord, &c.Viber, &c.Github, &c.Note, &aliases)
	c.Aliases = splitAliases(aliases)
	return c, err
}

// Aliases are stored as a newline-delimited TEXT blob in one column (the
// directory is small + personal, so a side table isn't worth it). splitAliases
// reads it back into trimmed, non-empty entries; joinAliases writes it.
func splitAliases(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinAliases(a []string) string { return strings.Join(a, "\n") }

// normalize trims every field and tidies the telegram handle (a bare username
// gets its leading @, a phone number is left as-is).
func normalize(c *client.Contact) {
	c.Name = strings.TrimSpace(c.Name)
	c.Phone = strings.TrimSpace(c.Phone)
	c.Email = strings.TrimSpace(c.Email)
	c.WhatsApp = strings.TrimSpace(c.WhatsApp)
	c.Discord = strings.TrimSpace(c.Discord)
	c.Viber = strings.TrimSpace(c.Viber)
	c.Github = strings.TrimPrefix(strings.TrimSpace(c.Github), "@")
	c.Note = strings.TrimSpace(c.Note)
	tg := strings.TrimSpace(c.Telegram)
	if tg != "" && !strings.HasPrefix(tg, "@") && !looksLikePhone(tg) {
		tg = "@" + tg
	}
	c.Telegram = tg
	ig := strings.TrimSpace(c.Instagram)
	if ig != "" && !strings.HasPrefix(ig, "@") {
		ig = "@" + ig
	}
	c.Instagram = ig
	c.Aliases = normAliases(c.Aliases)
}

// normAliases trims each alias, drops empties, and de-duplicates
// case-insensitively (keeping the first spelling seen).
func normAliases(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		k := strings.ToLower(a)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out
}

func looksLikePhone(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if s[0] == '+' {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// --- contacts CRUD -----------------------------------------------------------

// List returns every contact, ordered by name.
func (s *Store) List() ([]client.Contact, error) {
	return s.query(`SELECT ` + selectCols + ` FROM contacts ORDER BY name`)
}

// Get returns one contact by id.
func (s *Store) Get(id int64) (client.Contact, error) {
	return scanContact(s.db.QueryRow(`SELECT `+selectCols+` FROM contacts WHERE id=?`, id))
}

// Resolve returns contacts whose name OR any alias matches (case-insensitive,
// exact matches first then prefix matches), so callers can address a person by
// either their canonical name or a nickname. Aliases live in a delimited column,
// so the match is done in Go over the (small, personal) directory.
func (s *Store) Resolve(name string) ([]client.Contact, error) {
	name = strings.TrimSpace(name)
	all, err := s.List()
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(name)
	var exact, prefix []client.Contact
	for _, c := range all {
		hitExact, hitPrefix := false, false
		for _, cand := range append([]string{c.Name}, c.Aliases...) {
			lc := strings.ToLower(strings.TrimSpace(cand))
			if lc == want {
				hitExact = true
				break
			}
			if strings.HasPrefix(lc, want) {
				hitPrefix = true
			}
		}
		if hitExact {
			exact = append(exact, c)
		} else if hitPrefix {
			prefix = append(prefix, c)
		}
	}
	return append(exact, prefix...), nil
}

// ensureUnique enforces the directory-wide rule that no name or alias may be
// shared: a contact's own name + aliases must be internally distinct, and none
// may collide (case-insensitively) with any name or alias of another contact.
// id is the row being written (0 on create) so it can be excluded.
func (s *Store) ensureUnique(id int64, c client.Contact) error {
	mine := append([]string{c.Name}, c.Aliases...)
	seen := map[string]bool{}
	for _, t := range mine {
		k := strings.ToLower(strings.TrimSpace(t))
		if k == "" {
			continue
		}
		if seen[k] {
			return fmt.Errorf("%q is listed twice for this contact (name and aliases must be distinct)", strings.TrimSpace(t))
		}
		seen[k] = true
	}
	all, err := s.List()
	if err != nil {
		return err
	}
	taken := map[string]string{} // lower token -> owning contact's display name
	for _, e := range all {
		if e.ID == id {
			continue
		}
		for _, t := range append([]string{e.Name}, e.Aliases...) {
			if k := strings.ToLower(strings.TrimSpace(t)); k != "" {
				taken[k] = e.Name
			}
		}
	}
	for _, t := range mine {
		k := strings.ToLower(strings.TrimSpace(t))
		if k == "" {
			continue
		}
		if owner, ok := taken[k]; ok {
			return fmt.Errorf("%q already belongs to contact %q (names and aliases must be unique)", strings.TrimSpace(t), owner)
		}
	}
	return nil
}

func (s *Store) query(q string, args ...any) ([]client.Contact, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []client.Contact
	for rows.Next() {
		c, err := scanContact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Create inserts a contact, returning it with its new id.
func (s *Store) Create(_ Caller, c client.Contact) (client.Contact, error) {
	normalize(&c)
	if c.Name == "" {
		return c, fmt.Errorf("a contact needs a name")
	}
	if err := s.ensureUnique(0, c); err != nil {
		return c, err
	}
	res, err := s.db.Exec(
		`INSERT INTO contacts(name, phone, email, telegram, whatsapp, instagram, discord, viber, github, note, aliases, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		c.Name, c.Phone, c.Email, c.Telegram, c.WhatsApp, c.Instagram, c.Discord, c.Viber, c.Github, c.Note, joinAliases(c.Aliases), now(), now())
	if err != nil {
		return c, err
	}
	c.ID, _ = res.LastInsertId()
	return c, nil
}

// Update overwrites every channel field of a contact (addressed by c.ID).
func (s *Store) Update(_ Caller, c client.Contact) error {
	normalize(&c)
	if c.Name == "" {
		return fmt.Errorf("a contact needs a name")
	}
	if err := s.ensureUnique(c.ID, c); err != nil {
		return err
	}
	res, err := s.db.Exec(
		`UPDATE contacts SET name=?, phone=?, email=?, telegram=?, whatsapp=?, instagram=?, discord=?, viber=?, github=?, note=?, aliases=?, updated_at=?
		 WHERE id=?`,
		c.Name, c.Phone, c.Email, c.Telegram, c.WhatsApp, c.Instagram, c.Discord, c.Viber, c.Github, c.Note, joinAliases(c.Aliases), now(), c.ID)
	if err != nil {
		return err
	}
	return mustAffect(res, c.ID)
}

// Delete removes a contact.
func (s *Store) Delete(_ Caller, id int64) error {
	res, err := s.db.Exec(`DELETE FROM contacts WHERE id=?`, id)
	if err != nil {
		return err
	}
	return mustAffect(res, id)
}

// Upsert inserts or updates a set of contacts by name (bulk path for the agent
// and importers). An existing name is overwritten with the incoming fields.
func (s *Store) Upsert(caller Caller, cs []client.Contact) error {
	for _, c := range cs {
		existing, err := s.Resolve(c.Name)
		if err != nil {
			return err
		}
		var id int64
		for _, e := range existing {
			if strings.EqualFold(e.Name, c.Name) {
				id = e.ID
				break
			}
		}
		if id != 0 {
			c.ID = id
			if err := s.Update(caller, c); err != nil {
				return err
			}
		} else if _, err := s.Create(caller, c); err != nil {
			return err
		}
	}
	return nil
}

// --- helpers -----------------------------------------------------------------

func mustAffect(res sql.Result, id int64) error {
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("no contact with id %d", id)
	}
	return nil
}
