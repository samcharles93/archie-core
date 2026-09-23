package legacyimport

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/infrastructure/legacyread"
)

// blobColumns are the columns whose bytes must survive exactly: resource
// documents, and binding secrets, which stay decryptable by the configured
// keyring only if the envelope is untouched.
var blobColumns = map[string]string{
	"resources":        "value",
	"resource_history": "value",
	"bindings":         "secret",
}

// verify reads back what load wrote for lt and compares it with the source:
// the key set (a count passes on a swallowed row when ids have gaps), the
// (at, id) event order, blob bytes and the identity sequence.
func verify(ctx context.Context, tx pgx.Tx, lt loadedTable, problems *refusal) error {
	name := lt.table.Name
	ident := pgx.Identifier{name}.Sanitize()
	keyExprs := make([]string, len(lt.keyCols))
	for i, k := range lt.keyCols {
		keyExprs[i] = pgx.Identifier{k}.Sanitize() + "::text"
	}
	keyExpr := "concat_ws('/', " + strings.Join(keyExprs, ", ") + ")"

	targetKeys, err := strs(ctx, tx, "SELECT "+keyExpr+" FROM "+ident)
	if err != nil {
		return fmt.Errorf("legacy import: verify %s: %w", name, err)
	}
	if len(targetKeys) != len(lt.keys) {
		problems.add("%s: %d rows read, %d rows in the target", name, len(lt.keys), len(targetKeys))
	}
	if d := legacyread.CompareKeys(lt.keys, targetKeys); len(d.Missing)+len(d.Extra) > 0 {
		problems.add("%s: rows missing from the target %v, rows only in the target %v", name, d.Missing, d.Extra)
	}

	if name == "events" {
		if err := verifyEventOrder(ctx, tx, lt, problems); err != nil {
			return err
		}
	}
	if col, ok := blobColumns[name]; ok {
		if err := verifyBlobs(ctx, tx, lt, col, keyExpr, problems); err != nil {
			return err
		}
	}
	if lt.identity {
		return verifySequence(ctx, tx, name, problems)
	}
	return nil
}

// verifySequence asserts the next id the sequence hands out is above every
// imported id, reading its state rather than calling nextval.
func verifySequence(ctx context.Context, tx pgx.Tx, name string, problems *refusal) error {
	var seq string
	var maxID int64
	if err := tx.QueryRow(ctx, "SELECT pg_get_serial_sequence($1, 'id'), (SELECT COALESCE(max(id), 0) FROM "+
		pgx.Identifier{name}.Sanitize()+")", name).Scan(&seq, &maxID); err != nil {
		return fmt.Errorf("legacy import: verify %s sequence: %w", name, err)
	}
	var (
		last   int64
		called bool
	)
	// seq comes from pg_get_serial_sequence, which returns it quoted.
	if err := tx.QueryRow(ctx, "SELECT last_value, is_called FROM "+seq).Scan(&last, &called); err != nil {
		return fmt.Errorf("legacy import: verify %s sequence: %w", name, err)
	}
	next := last
	if called {
		next++
	}
	if next <= maxID {
		problems.add("%s: next id %d would reuse an imported id (max %d)", name, next, maxID)
	}
	return nil
}

// verifyEventOrder compares the ids in (at, id) order on both sides. The
// source was read in that order; a difference means microsecond storage
// reordered history, and a cursor reader would skip a row.
func verifyEventOrder(ctx context.Context, tx pgx.Tx, lt loadedTable, problems *refusal) error {
	source := make([]int64, len(lt.keys))
	for i, k := range lt.keys {
		id, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			return fmt.Errorf("legacy import: event key %q: %w", k, err)
		}
		source[i] = id
	}
	rows, err := tx.Query(ctx, "SELECT id FROM events ORDER BY at, id")
	if err != nil {
		return fmt.Errorf("legacy import: verify events order: %w", err)
	}
	target, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return fmt.Errorf("legacy import: verify events order: %w", err)
	}
	if d := legacyread.CompareOrder(source, target); d != nil {
		problems.add("events: (at, id) order differs at position %d: source id %d, target id %d", d.Position, d.Source, d.Target)
	}
	return nil
}

func verifyBlobs(ctx context.Context, tx pgx.Tx, lt loadedTable, col, keyExpr string, problems *refusal) error {
	source := map[string][]byte{}
	for i, k := range lt.keys {
		switch v := lt.rows.Value(i, col).(type) {
		case []byte:
			source[k] = v
		case string:
			source[k] = []byte(v)
		}
	}
	rows, err := tx.Query(ctx, fmt.Sprintf("SELECT %s, %s FROM %s", keyExpr,
		pgx.Identifier{col}.Sanitize(), pgx.Identifier{lt.table.Name}.Sanitize()))
	if err != nil {
		return fmt.Errorf("legacy import: verify %s.%s: %w", lt.table.Name, col, err)
	}
	target := map[string][]byte{}
	for rows.Next() {
		var (
			k string
			v []byte
		)
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return fmt.Errorf("legacy import: verify %s.%s: %w", lt.table.Name, col, err)
		}
		target[k] = v
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("legacy import: verify %s.%s: %w", lt.table.Name, col, err)
	}
	if diff := legacyread.CompareBlobs(source, target); len(diff) > 0 {
		problems.add("%s.%s: bytes differ from the source for %v", lt.table.Name, col, diff)
	}
	return nil
}

// unindexedMessages returns the source message ids the FTS5 index missed.
func unindexedMessages(ctx context.Context, gw *legacyread.Source, loaded []loadedTable) ([]string, error) {
	indexed, err := gw.IndexedMessageIDs(ctx)
	if err != nil {
		return nil, err
	}
	for _, lt := range loaded {
		if lt.table.Name == "messages" {
			return legacyread.CompareKeys(indexed, lt.keys).Extra, nil
		}
	}
	return nil, nil
}

func strs(ctx context.Context, tx pgx.Tx, query string) ([]string, error) {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}
