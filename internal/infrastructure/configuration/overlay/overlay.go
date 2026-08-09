// Package overlay persists runtime-tunable configuration overrides in
// their own SQLite file (cfg.DBPath + "-config.sqlite"), separate from
// the task, conversation and search stores so each file owns its
// user_version and its migrator with no contention.
//
// It backs the dashboard's config editing: a PATCH writes here, the
// daemon layers these values over the file config at boot and on every
// reload, and removing the file (or passing --no-config-overlay /
// ARCHIE_SKIP_CONFIG_OVERLAY=1) is the documented recovery from a write
// that would otherwise break boot.
package overlay

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DeniedKeys are the config keys the overlay may never set, mapped to
// the reason shown to the operator. db_path is the daemon's own
// bootstrap path -- the daemon must read it before it can open this
// store -- and work_dir pins the whole working layout. Enforced at
// write time (the API returns 4xx) and again in Set, not silently
// dropped at read time.
var DeniedKeys = map[string]string{
	"db_path":  "required for bootstrap; cannot be changed at runtime",
	"work_dir": "pins the daemon's working layout; cannot be changed at runtime",
}

// Store is the config overlay database.
type Store struct{ db *sql.DB }

const schemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS config_overlay (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	updated_by TEXT NOT NULL
);
`

// Open opens (creating if needed) the overlay database and migrates it.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("overlay: init schema: %w", err), db.Close())
	}
	if err := migrate(ctx, db); err != nil {
		return nil, errors.Join(fmt.Errorf("overlay: migrate: %w", err), db.Close())
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// sqliteDSN enables WAL and a busy timeout. Kept local to match the
// task and gateway stores; deduplicating the three copies is a
// follow-up, not part of this change.
func sqliteDSN(path string) string {
	separator := "?"
	if strings.HasSuffix(path, "?") || strings.HasSuffix(path, "&") {
		separator = ""
	} else if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
}

func migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > schemaVersion {
		return fmt.Errorf("config overlay schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if version == schemaVersion {
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `PRAGMA user_version = 1`); err != nil {
		return err
	}
	return tx.Commit()
}

// Set persists one override. key is a dotted config path
// ("budgets.max_steps"); value is the JSON encoding of the field value
// ("12", "\"60s\"", "true"). updatedBy names the writer for audit.
// Keys in DeniedKeys are refused here as well as at the API layer.
func (s *Store) Set(ctx context.Context, key, value, updatedBy string) error {
	if reason, denied := DeniedKeys[key]; denied {
		return fmt.Errorf("config key %s is not runtime-tunable: %s", key, reason)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO config_overlay (key, value, updated_at, updated_by) VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET
		   value = excluded.value, updated_at = excluded.updated_at, updated_by = excluded.updated_by`,
		key, value, time.Now().UTC().Format(time.RFC3339), updatedBy)
	return err
}

// Delete removes one override, restoring the file's value for that key.
func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM config_overlay WHERE key = ?`, key)
	return err
}

// Snapshot returns every stored override as a nested map of typed
// values ready to decode into config.Config. Dotted keys are nested:
// "budgets.max_steps" becomes map["budgets"]["max_steps"]. Values are
// JSON-decoded so the caller never has to know the field's type.
func (s *Store) Snapshot(ctx context.Context) (map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM config_overlay`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]any)
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("overlay: key %s holds invalid JSON %q: %w", key, raw, err)
		}
		if err := Nest(out, key, value); err != nil {
			return nil, err
		}
	}
	return out, rows.Err()
}

// Nest inserts value into root at the dotted key path ("a.b.c" ->
// root["a"]["b"]["c"]), creating intermediate maps as needed.
func Nest(root map[string]any, key string, value any) error {
	parts := strings.Split(key, ".")
	if len(parts) == 0 || parts[0] == "" {
		return fmt.Errorf("overlay: empty key %q", key)
	}
	cur := root
	for _, part := range parts[:len(parts)-1] {
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = make(map[string]any)
			cur[part] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = value
	return nil
}
