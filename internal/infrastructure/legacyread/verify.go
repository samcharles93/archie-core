package legacyread

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// KeyDiff is the difference between two key sets. A count comparison alone
// passes on a swallowed row when ids have gaps; comparing the sets does not.
type KeyDiff struct {
	Missing []string // in the source, not the target
	Extra   []string // in the target, not the source
}

// CompareKeys compares the source's key set with the target's.
func CompareKeys(source, target []string) KeyDiff {
	var d KeyDiff
	src := setOf(source)
	tgt := setOf(target)
	for k := range src {
		if !tgt[k] {
			d.Missing = append(d.Missing, k)
		}
	}
	for k := range tgt {
		if !src[k] {
			d.Extra = append(d.Extra, k)
		}
	}
	slices.Sort(d.Missing)
	slices.Sort(d.Extra)
	return d
}

func setOf(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	return set
}

// Divergence is the first position at which two ordered id sequences differ.
// A missing element on one side reads as 0.
type Divergence struct {
	Position       int
	Source, Target int64
}

// CompareOrder compares two id sequences read in cursor order, returning nil
// when they are identical.
func CompareOrder(source, target []int64) *Divergence {
	for i := range max(len(source), len(target)) {
		var s, t int64
		if i < len(source) {
			s = source[i]
		}
		if i < len(target) {
			t = target[i]
		}
		if s != t || i >= len(source) || i >= len(target) {
			return &Divergence{Position: i, Source: s, Target: t}
		}
	}
	return nil
}

// Collision is two events whose instants differ only below a microsecond
// while their ids run the other way. Postgres stores microseconds, so its
// (at, id) order would put LaterID before EarlierID.
type Collision struct {
	EarlierID, LaterID int64
	At                 time.Time // the shared microsecond
}

// eventAtLayouts are the layouts events.at has been written in, newest first.
var eventAtLayouts = []string{"2006-01-02T15:04:05.000000000Z", time.RFC3339Nano, time.RFC3339}

func parseEventAt(s string) (time.Time, error) {
	for _, layout := range eventAtLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp %q", s)
}

// MicrosecondCollisions returns every event pair that truncating events.at
// to microseconds would reorder.
func (s *Source) MicrosecondCollisions(ctx context.Context) ([]Collision, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, at FROM events ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("legacyread: events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type event struct {
		id int64
		at time.Time
	}
	byMicro := map[time.Time][]event{}
	for rows.Next() {
		var (
			id  int64
			raw string
		)
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, fmt.Errorf("legacyread: events: %w", err)
		}
		at, err := parseEventAt(raw)
		if err != nil {
			return nil, fmt.Errorf("legacyread: event %d: %w", id, err)
		}
		micro := at.Truncate(time.Microsecond)
		byMicro[micro] = append(byMicro[micro], event{id, at})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("legacyread: events: %w", err)
	}
	var out []Collision
	for micro, events := range byMicro {
		for i, a := range events {
			for _, b := range events[i+1:] {
				// Read in id order, so a.id < b.id.
				if b.at.Before(a.at) {
					out = append(out, Collision{EarlierID: b.id, LaterID: a.id, At: micro})
				}
			}
		}
	}
	slices.SortFunc(out, func(x, y Collision) int { return int(x.EarlierID - y.EarlierID) })
	return out, nil
}

// ForeignKey is a reference SQLite declared but never enforced (the State
// Store never set PRAGMA foreign_keys) and Postgres will.
type ForeignKey struct {
	Table, Column   string
	KeyExpr         string // SQL naming the child row in a report
	Parent, ParentC string
}

// IdentityForeignKeys are the three identity references.
var IdentityForeignKeys = []ForeignKey{
	{Table: "identity_aliases", Column: "identity_id", KeyExpr: "alias", Parent: "identities", ParentC: "id"},
	{Table: "identity_events", Column: "identity_id", KeyExpr: "CAST(id AS TEXT)", Parent: "identities", ParentC: "id"},
	{Table: "identity_subjects", Column: "identity_id", KeyExpr: "issuer || '/' || subject", Parent: "identities", ParentC: "id"},
}

// Orphan is a child row whose parent does not exist.
type Orphan struct {
	Key, Parent string
}

// Orphans returns fk's child rows with no parent.
func (s *Source) Orphans(ctx context.Context, fk ForeignKey) ([]Orphan, error) {
	query := fmt.Sprintf("SELECT %s, %s FROM %s WHERE %s NOT IN (SELECT %s FROM %s) ORDER BY 1",
		fk.KeyExpr, fk.Column, fk.Table, fk.Column, fk.ParentC, fk.Parent)
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("legacyread: orphans in %s: %w", fk.Table, err)
	}
	defer func() { _ = rows.Close() }()
	var out []Orphan
	for rows.Next() {
		var o Orphan
		if err := rows.Scan(&o.Key, &o.Parent); err != nil {
			return nil, fmt.Errorf("legacyread: orphans in %s: %w", fk.Table, err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Duplicate is a key the target will hold unique that more than one source
// row shares.
type Duplicate struct {
	Key    string
	Values []string
}

// DuplicateAliases groups identity aliases by Unicode lower case. SQLite's
// NOCASE folds ASCII only, so aliases that differ in non-ASCII case coexist in
// the source and collide on the target's lower(alias) index.
func (s *Source) DuplicateAliases(ctx context.Context) ([]Duplicate, error) {
	aliases, err := s.strings(ctx, "SELECT alias FROM identity_aliases ORDER BY alias")
	if err != nil {
		return nil, err
	}
	return duplicates(aliases, aliases, strings.ToLower), nil
}

// DuplicateBindings groups bindings by source. One binding per source is
// enforced in Go, not by the legacy schema, so a duplicate is possible and is
// refused rather than repaired (D4.2).
func (s *Source) DuplicateBindings(ctx context.Context) ([]Duplicate, error) {
	rows, err := s.Read(ctx, Table{Name: "bindings", Columns: []string{"id", "source"}, OrderBy: "id"})
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(rows.Rows))
	sources := make([]string, len(rows.Rows))
	for i := range rows.Rows {
		ids[i] = fmt.Sprint(rows.Value(i, "id"))
		sources[i] = fmt.Sprint(rows.Value(i, "source"))
	}
	return duplicates(sources, ids, func(s string) string { return s }), nil
}

// duplicates groups values by key(keys[i]) and returns the groups of two or
// more, sorted by key.
func duplicates(keys, values []string, key func(string) string) []Duplicate {
	groups := map[string][]string{}
	for i, k := range keys {
		groups[key(k)] = append(groups[key(k)], values[i])
	}
	var out []Duplicate
	for _, k := range slices.Sorted(maps.Keys(groups)) {
		if len(groups[k]) > 1 {
			out = append(out, Duplicate{Key: k, Values: groups[k]})
		}
	}
	return out
}

// Sequence is an identity column's state: the highest id present and the id
// SQLite would assign next. Next comes from AUTOINCREMENT's high-water mark,
// so a deleted top row still reserves its id.
type Sequence struct {
	Table       string
	MaxID, Next int64
}

// sequenceTables are the State Store tables with an AUTOINCREMENT id.
var sequenceTables = []string{"tasks", "transitions", "events", "resource_history", "identity_events"}

// Sequences returns the state of every State Store identity column.
func (s *Source) Sequences(ctx context.Context) ([]Sequence, error) {
	out := make([]Sequence, 0, len(sequenceTables))
	for _, table := range sequenceTables {
		seq, err := s.SequenceOf(ctx, table)
		if err != nil {
			return nil, err
		}
		out = append(out, seq)
	}
	return out, nil
}

// CompareBlobs returns, sorted, every key whose bytes differ between source
// and target or that only one side holds.
func CompareBlobs(source, target map[string][]byte) []string {
	var out []string
	for k, v := range source {
		if t, ok := target[k]; !ok || !bytes.Equal(v, t) {
			out = append(out, k)
		}
	}
	for k := range target {
		if _, ok := source[k]; !ok {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}
