package contacts

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zouriel/zcoms/client"
)

func openTmp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "comms.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestAliases(t *testing.T) {
	s := openTmp(t)

	c, err := s.Create(Owner, client.Contact{
		Name:    "Ali",
		Aliases: []string{"Aliyya", "  Ali bro ", "aliyya"}, // dup (case) + whitespace
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(c.Aliases) != 2 {
		t.Fatalf("aliases not de-duped/trimmed: %#v", c.Aliases)
	}

	// Aliases survive a round-trip through the store.
	got, err := s.Get(c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Aliases) != 2 || got.Aliases[0] != "Aliyya" {
		t.Fatalf("aliases not persisted: %#v", got.Aliases)
	}
}

func TestResolveByAlias(t *testing.T) {
	s := openTmp(t)
	c, _ := s.Create(Owner, client.Contact{Name: "Robert", Aliases: []string{"Bob"}})

	for _, q := range []string{"bob", "BOB", "Bob"} {
		hits, err := s.Resolve(q)
		if err != nil {
			t.Fatalf("resolve %q: %v", q, err)
		}
		if len(hits) != 1 || hits[0].ID != c.ID {
			t.Fatalf("resolve %q = %#v, want Robert", q, hits)
		}
	}
}

func TestUniqueNamesAndAliases(t *testing.T) {
	s := openTmp(t)
	if _, err := s.Create(Owner, client.Contact{Name: "Alice", Aliases: []string{"Ali"}}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// New contact whose name collides with an existing alias.
	if _, err := s.Create(Owner, client.Contact{Name: "Ali"}); err == nil {
		t.Fatal("expected name-vs-alias collision to be rejected")
	}
	// New contact whose alias collides with an existing name.
	if _, err := s.Create(Owner, client.Contact{Name: "Bob", Aliases: []string{"alice"}}); err == nil {
		t.Fatal("expected alias-vs-name collision to be rejected")
	}
	// Internal duplicate (name repeated as its own alias).
	if _, err := s.Create(Owner, client.Contact{Name: "Carl", Aliases: []string{"carl"}}); err == nil {
		t.Fatal("expected internal name/alias duplicate to be rejected")
	}
	// A genuinely unique contact is fine.
	bob, err := s.Create(Owner, client.Contact{Name: "Bob", Aliases: []string{"Bobby"}})
	if err != nil {
		t.Fatalf("unique create rejected: %v", err)
	}
	// Updating a contact keeping its own name/aliases must not self-collide.
	bob.Phone = "+1"
	if err := s.Update(Owner, bob); err != nil {
		t.Fatalf("self-update rejected: %v", err)
	}
}

// A live comms.db keeps columns for channels that were retired. They are
// never read, and a contact that has one still
// resolves on the channels that remain.
func TestLegacyChannelColumnsAreIgnored(t *testing.T) {
	s := openTmp(t)
	for _, col := range []string{"retired_a", "retired_b"} {
		if _, err := s.db.Exec(`ALTER TABLE contacts ADD COLUMN ` + col + ` TEXT`); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			t.Fatalf("add %s: %v", col, err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO contacts(name, phone, retired_a, retired_b) VALUES('Raani', '+9609752353', '@r', 'x1')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got, err := s.Resolve("Raani")
	if err != nil || len(got) != 1 {
		t.Fatalf("resolve: %v %v", got, err)
	}
	if a := got[0].Address("whatsapp"); a != "+9609752353" {
		t.Fatalf("whatsapp address %q, want the phone", a)
	}
	if a := got[0].Address("retired_a"); a != "" {
		t.Fatalf("retired channel address %q, want none", a)
	}
}
