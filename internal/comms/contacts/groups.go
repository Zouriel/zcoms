package contacts

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/client"
)

// The group registry lives in comms.db beside the contacts. A row is created
// when a group is created or listed; listen and follow default off and only
// the owner turns them on (the hub refuses group_set from the agent).
// group_calls records which messages were forwarded to the agent as calls, so
// an agent-caller read can be limited to a window around a recent call.

func (s *Store) migrateGroups() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS groups (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  transport  TEXT NOT NULL,
  native_id  TEXT NOT NULL,
  aliases    TEXT,
  listen     INTEGER NOT NULL DEFAULT 0,
  follow     INTEGER NOT NULL DEFAULT 0,
  created_at TEXT,
  updated_at TEXT,
  UNIQUE(transport, native_id)
);
CREATE TABLE IF NOT EXISTS group_calls (
  transport TEXT NOT NULL,
  native_id TEXT NOT NULL,
  msg_id    TEXT NOT NULL,
  at        INTEGER NOT NULL,
  PRIMARY KEY(transport, native_id, msg_id)
);`)
	return err
}

// ErrNoGroup is returned when no registry row matches.
var ErrNoGroup = errors.New("no such group")

const groupCols = `id, name, transport, native_id, COALESCE(aliases,''), listen, follow`

func scanGroup(sc interface{ Scan(...any) error }) (client.Group, error) {
	var g client.Group
	var aliases string
	var listen, follow int
	err := sc.Scan(&g.ID, &g.Name, &g.Transport, &g.NativeID, &aliases, &listen, &follow)
	g.Aliases = splitAliases(aliases)
	g.Listen = listen != 0
	g.Follow = follow != 0
	return g, err
}

// UpsertGroup records a group seen on a transport, refreshing its name. The
// flags and aliases of an existing row are kept.
func (s *Store) UpsertGroup(transportName, nativeID, name string) (client.Group, error) {
	nativeID = strings.TrimSpace(nativeID)
	if transportName == "" || nativeID == "" {
		return client.Group{}, fmt.Errorf("a group needs a transport and a native id")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = nativeID
	}
	if _, err := s.db.Exec(`INSERT INTO groups(name, transport, native_id, created_at, updated_at) VALUES(?,?,?,?,?)
		ON CONFLICT(transport, native_id) DO UPDATE SET name=excluded.name, updated_at=excluded.updated_at`,
		name, transportName, nativeID, now(), now()); err != nil {
		return client.Group{}, err
	}
	return s.GroupByNative(transportName, nativeID)
}

// GroupByNative returns the registry row for a native id, or ErrNoGroup.
func (s *Store) GroupByNative(transportName, nativeID string) (client.Group, error) {
	g, err := scanGroup(s.db.QueryRow(`SELECT `+groupCols+` FROM groups WHERE transport=? AND native_id=?`,
		transportName, strings.TrimSpace(nativeID)))
	if errors.Is(err, sql.ErrNoRows) {
		return client.Group{}, ErrNoGroup
	}
	return g, err
}

// Groups lists the registry for a transport ("" = every transport).
func (s *Store) Groups(transportName string) ([]client.Group, error) {
	q := `SELECT ` + groupCols + ` FROM groups`
	var args []any
	if transportName != "" {
		q += ` WHERE transport=?`
		args = append(args, transportName)
	}
	rows, err := s.db.Query(q+` ORDER BY name COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []client.Group
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ResolveGroups matches a name or alias against a transport's groups, exact
// (case-insensitive) matches first, then prefix matches.
func (s *Store) ResolveGroups(transportName, name string) (exact, prefix []client.Group, err error) {
	all, err := s.Groups(transportName)
	if err != nil {
		return nil, nil, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	if want == "" {
		return nil, nil, nil
	}
	for _, g := range all {
		hitExact, hitPrefix := false, false
		for _, cand := range append([]string{g.Name}, g.Aliases...) {
			lc := strings.ToLower(strings.TrimSpace(cand))
			if lc == want {
				hitExact = true
				break
			}
			if strings.HasPrefix(lc, want) {
				hitPrefix = true
			}
		}
		switch {
		case hitExact:
			exact = append(exact, g)
		case hitPrefix:
			prefix = append(prefix, g)
		}
	}
	return exact, prefix, nil
}

// SetGroupFlags changes listen and/or follow (nil leaves one unchanged). Only
// an owner caller may change them.
func (s *Store) SetGroupFlags(caller Caller, transportName, nativeID string, listen, follow *bool) (client.Group, error) {
	if caller != Owner {
		return client.Group{}, fmt.Errorf("only the owner can change a group's listen or follow flags")
	}
	g, err := s.GroupByNative(transportName, nativeID)
	if err != nil {
		return g, err
	}
	if listen != nil {
		g.Listen = *listen
	}
	if follow != nil {
		g.Follow = *follow
	}
	_, err = s.db.Exec(`UPDATE groups SET listen=?, follow=?, updated_at=? WHERE id=?`,
		boolInt(g.Listen), boolInt(g.Follow), now(), g.ID)
	return g, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// RecordCall notes that msgID in a group was forwarded to the agent as a call,
// and prunes records older than a day.
func (s *Store) RecordCall(transportName, nativeID, msgID string, at time.Time) error {
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO group_calls(transport, native_id, msg_id, at) VALUES(?,?,?,?)`,
		transportName, nativeID, msgID, at.Unix()); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM group_calls WHERE at < ?`, time.Now().Add(-48*time.Hour).Unix())
	return err
}

// RecentCall reports whether msgID was forwarded as a call within window.
func (s *Store) RecentCall(transportName, nativeID, msgID string, window time.Duration) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM group_calls WHERE transport=? AND native_id=? AND msg_id=? AND at >= ?`,
		transportName, nativeID, msgID, time.Now().Add(-window).Unix()).Scan(&n)
	return n > 0, err
}
