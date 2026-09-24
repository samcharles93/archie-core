package legacyimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/infrastructure/legacyread"
)

// droppedColumns are legacy columns with no target column by design:
// PocketBase gave every ledger row a random record id, and the Postgres
// ledgers are keyed by their natural unique key instead.
var droppedColumns = map[string]bool{
	"binding_dispatches.id":  true,
	"playbook_dispatches.id": true,
}

// newColumns are target columns added after the legacy stores, with no
// legacy value to carry. Imported rows take the column default: an imported
// capture has no event type, so it stays unidentified and never dispatches;
// an imported mapping has no event type, so no binding using it dispatches
// until it is given one; an imported binding has no filter.
var newColumns = map[string]bool{
	"captures.event_type": true,
	"mappings.event_type": true,
	"bindings.filter":     true,
}

// targetKeys override a table's legacy key where the target has no column
// for it.
var targetKeys = map[string][]string{
	"binding_dispatches":  {"binding", "capture"},
	"playbook_dispatches": {"playbook_id", "playbook_version", "event_id", "action_id"},
}

// jsonColumns are PocketBase JSON fields, which may be NULL in the source and
// map to a NOT NULL text column defaulting to the empty string. unwrap marks the fields the legacy reader
// decoded from a JSON string (jsonTextToString) rather than returning raw.
var jsonColumns = map[string]struct{ unwrap bool }{
	"captures.headers":  {},
	"mappings.fields":   {},
	"tool_calls.args":   {unwrap: true},
	"tool_calls.result": {unwrap: true},
}

// timeLayouts are every layout a legacy timestamp was written in: the
// fixed-width cursor layout, Go's RFC 3339 forms, PocketBase's autodate
// layout and SQLite's datetime('now').
var timeLayouts = []string{
	"2006-01-02T15:04:05.000000000Z",
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
}

type column struct {
	name     string
	dataType string
	identity bool
	source   int // index into the legacy row
}

// loadedTable is what load wrote, for verify to check.
type loadedTable struct {
	table    legacyread.Table
	rows     legacyread.Rows
	keys     []string // target keys of the rows written, in source read order
	keyCols  []string
	identity bool
}

// load reads t from src, converts every row to the target's types and copies
// them in with their historical ids. A row it cannot convert is added to
// problems and nothing of the table is written.
func load(ctx context.Context, tx pgx.Tx, src *legacyread.Source, t legacyread.Table, problems *refusal) (loadedTable, error) {
	cols, err := plan(ctx, tx, t, problems)
	if err != nil || cols == nil {
		return loadedTable{table: t}, err
	}
	rows, err := src.Read(ctx, t)
	if err != nil {
		return loadedTable{}, err
	}
	lt := loadedTable{table: t, rows: rows, keyCols: t.Key}
	if k, ok := targetKeys[t.Name]; ok {
		lt.keyCols = k
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.name
		lt.identity = lt.identity || c.identity
	}
	out, ok := convertRows(lt, cols, names, problems)
	if !ok {
		return lt, nil
	}
	for _, row := range out {
		key := make([]string, len(lt.keyCols))
		for j, k := range lt.keyCols {
			key[j] = fmt.Sprint(row[slices.Index(names, k)])
		}
		lt.keys = append(lt.keys, strings.Join(key, "/"))
	}
	if len(out) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{t.Name}, names, pgx.CopyFromRows(out)); err != nil {
			problems.add("%s: the target rejected the rows: %v", t.Name, err)
			return lt, nil
		}
	}
	if lt.identity {
		return lt, advanceSequence(ctx, tx, src, t.Name)
	}
	return lt, nil
}

// convertRows converts every row of lt, adding each value it cannot convert
// to problems.
func convertRows(lt loadedTable, cols []column, names []string, problems *refusal) ([][]any, bool) {
	sourceKeys := lt.rows.Keys()
	out := make([][]any, 0, len(lt.rows.Rows))
	ok := true
	for i, row := range lt.rows.Rows {
		converted := make([]any, len(cols))
		for j, c := range cols {
			v, err := convert(lt.table.Name+"."+c.name, c.dataType, row[c.source])
			if err != nil {
				problems.add("%s %s: column %s value %s: %v", lt.table.Name, sourceKeys[i], names[j], quote(row[c.source]), err)
				ok = false
			}
			converted[j] = v
		}
		out = append(out, converted)
	}
	return out, ok
}

// advanceSequence sets table's identity sequence to hand out the id the
// legacy AUTOINCREMENT would have, even for a table whose rows were all
// deleted. is_called = false makes that id the next one, which keeps
// AUTOINCREMENT's promise that a deleted top id is never reused.
func advanceSequence(ctx context.Context, tx pgx.Tx, src *legacyread.Source, table string) error {
	seq, err := src.SequenceOf(ctx, table)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SELECT setval(pg_get_serial_sequence($1, 'id'), $2, false)", table, seq.Next); err != nil {
		return fmt.Errorf("legacy import: advance %s sequence: %w", table, err)
	}
	return nil
}

// plan matches the target table's columns to the legacy ones. A column on
// either side with no counterpart is a schema gap the import refuses to
// guess about; it returns nil columns then.
func plan(ctx context.Context, tx pgx.Tx, t legacyread.Table, problems *refusal) ([]column, error) {
	rows, err := tx.Query(ctx, `SELECT column_name, data_type, is_identity = 'YES'
		FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND is_generated = 'NEVER'
		ORDER BY ordinal_position`, t.Name)
	if err != nil {
		return nil, fmt.Errorf("legacy import: %s columns: %w", t.Name, err)
	}
	cols, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (column, error) {
		var c column
		err := row.Scan(&c.name, &c.dataType, &c.identity)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("legacy import: %s columns: %w", t.Name, err)
	}
	gap := len(cols) == 0
	if gap {
		problems.add("%s: no target table", t.Name)
	}
	targetNames := map[string]bool{}
	cols = slices.DeleteFunc(cols, func(c column) bool { return newColumns[t.Name+"."+c.name] })
	for i := range cols {
		targetNames[cols[i].name] = true
		cols[i].source = slices.Index(t.Columns, cols[i].name)
		if cols[i].source < 0 {
			problems.add("%s.%s: the target column has no legacy column", t.Name, cols[i].name)
			gap = true
		}
	}
	for _, c := range t.Columns {
		if !targetNames[c] && !droppedColumns[t.Name+"."+c] {
			problems.add("%s.%s: the legacy column has no target column", t.Name, c)
			gap = true
		}
	}
	if gap {
		return nil, nil
	}
	return cols, nil
}

// convert maps one SQLite value to the Go value pgx writes to a column of
// dataType. It never coerces: a value the column cannot hold is an error
// naming the rule it breaks.
func convert(qualified, dataType string, v any) (any, error) {
	if j, ok := jsonColumns[qualified]; ok {
		return convertJSON(v, j.unwrap)
	}
	if v == nil {
		return nil, fmt.Errorf("NULL in a NOT NULL %s column", dataType)
	}
	conv, ok := converters[dataType]
	if !ok {
		return nil, fmt.Errorf("unsupported target type %s", dataType)
	}
	return conv(v)
}

func convertJSON(v any, unwrap bool) (any, error) {
	switch s := v.(type) {
	case nil:
		return "", nil
	case string:
		if unwrap {
			var decoded string
			if json.Unmarshal([]byte(s), &decoded) == nil {
				return decoded, nil
			}
		}
		return s, nil
	}
	return nil, errors.New("text: not a string")
}

// converters are keyed by information_schema data_type.
var converters = map[string]func(any) (any, error){
	"text": func(v any) (any, error) {
		if s, ok := v.(string); ok {
			return s, nil
		}
		return nil, errors.New("text: not a string")
	},
	"bigint": func(v any) (any, error) {
		switch n := v.(type) {
		case int64:
			return n, nil
		case float64:
			if n == math.Trunc(n) && math.Abs(n) < 1<<63 {
				return int64(n), nil
			}
		}
		return nil, errors.New("bigint: not an integer")
	},
	"boolean": func(v any) (any, error) {
		switch b := v.(type) {
		case bool:
			return b, nil
		case int64:
			if b == 0 || b == 1 {
				return b == 1, nil
			}
		}
		return nil, errors.New("boolean: not 0 or 1")
	},
	"bytea": func(v any) (any, error) {
		switch b := v.(type) {
		case []byte:
			return b, nil
		case string:
			return []byte(b), nil
		}
		return nil, errors.New("bytea: not a blob")
	},
	"timestamp with time zone": func(v any) (any, error) {
		if s, ok := v.(string); ok {
			for _, layout := range timeLayouts {
				if at, err := time.Parse(layout, s); err == nil {
					return at.UTC(), nil
				}
			}
		}
		return nil, errors.New("timestamptz: not a timestamp in any legacy layout")
	},
}

func quote(v any) string {
	switch s := v.(type) {
	case nil:
		return "NULL"
	case string:
		return fmt.Sprintf("%q", s)
	case []byte:
		return fmt.Sprintf("x'%x'", s)
	}
	return fmt.Sprint(v)
}
