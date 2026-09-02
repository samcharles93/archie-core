package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/binding"
)

const bindingsSchema = `
CREATE TABLE IF NOT EXISTS bindings (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	name          TEXT NOT NULL DEFAULT '',
	source        TEXT NOT NULL DEFAULT '',
	mapping_id    INTEGER NOT NULL DEFAULT 0,
	workflow      TEXT NOT NULL DEFAULT '',
	version       INTEGER NOT NULL DEFAULT 1,
	status        TEXT NOT NULL DEFAULT 'draft',
	secret        TEXT NOT NULL DEFAULT '',
	created_at    TEXT NOT NULL,
	updated_at    TEXT NOT NULL,
	CHECK (status IN ('draft','pending_approval','armed'))
);
CREATE INDEX IF NOT EXISTS idx_bindings_source_status ON bindings(source, status);
CREATE TABLE IF NOT EXISTS binding_dispatches (
	binding_id     INTEGER NOT NULL,
	binding_version INTEGER NOT NULL,
	capture_id     INTEGER NOT NULL,
	task_id        INTEGER NOT NULL,
	dispatched_at  TEXT NOT NULL,
	PRIMARY KEY (binding_id, capture_id)
);
`

// ErrBindingNotFound is returned by UpdateBinding and DeleteBinding when no
// row matches the given ID. GetBinding uses (nil, nil) instead, matching
// TaskByID's convention -- "not found" is the normal answer to "does a
// binding with this ID currently exist," not an error.
var ErrBindingNotFound = errors.New("store: binding not found")

// ErrBindingOverlap is returned by InsertBinding and UpdateBinding when a
// different binding already covers the same source. Two bindings for one
// source would race over the same inbound webhook; the store refuses rather
// than picking an arbitrary winner.
var ErrBindingOverlap = errors.New("store: binding overlaps existing binding for source")

// ErrBindingTransition is returned by ApproveBinding when the from-status
// is anything other than pending_approval. Approve is the only sanctioned
// transition into armed, and the guard is the SQL WHERE clause, not a
// separate read.
var ErrBindingTransition = errors.New("store: binding state transition rejected")

// ErrAlreadyDispatched is returned by RecordDispatch when the (binding_id,
// capture_id) pair already exists in binding_dispatches. The dedup ledger is
// the at-most-once guarantee per capture; the caller is expected to surface
// this to the dispatch loop rather than retry.
var ErrAlreadyDispatched = errors.New("store: binding already dispatched for capture")

// bindingTimeLayout follows captureTimeLayout's reasoning: fixed-width
// RFC3339 so string and chronological order agree.
const bindingTimeLayout = time.RFC3339

// InsertBinding persists a new binding in status=draft regardless of the
// caller's Status field, stamps CreatedAt/UpdatedAt to now, and seeds
// Version=1. A pre-insert overlap check refuses a duplicate source in the
// same transaction; ErrBindingOverlap is returned rather than a silent
// constraint violation.
func (s *Store) InsertBinding(ctx context.Context, b binding.Binding) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var overlapID int64
	row := tx.QueryRowContext(ctx, `SELECT id FROM bindings WHERE source = ? LIMIT 1`, b.Matcher.Source)
	if err := row.Scan(&overlapID); err == nil {
		return 0, ErrBindingOverlap
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}

	now := time.Now().UTC().Format(bindingTimeLayout)
	secret, err := s.encryptBindingSecret(b.Secret)
	if err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, `
		INSERT INTO bindings (name, source, mapping_id, workflow, version, status, secret, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, 'draft', ?, ?, ?)`,
		b.Name, b.Matcher.Source, b.MappingID, b.Workflow, secret, now, now)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// GetBinding returns the binding with the given ID, or (nil, nil) if none
// exists.
func (s *Store) GetBinding(ctx context.Context, id int64) (*binding.Binding, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, source, mapping_id, workflow, version, status, secret, created_at, updated_at
		FROM bindings WHERE id=?`, id)
	b, err := scanBindingRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.decryptBindingSecret(b); err != nil {
		return nil, err
	}
	return b, nil
}

// ListBindings returns every binding, newest first.
func (s *Store) ListBindings(ctx context.Context) (out []binding.Binding, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, source, mapping_id, workflow, version, status, secret, created_at, updated_at
		FROM bindings ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { retErr = errors.Join(retErr, rows.Close()) }()

	for rows.Next() {
		b, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		if err := s.decryptBindingSecret(b); err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// UpdateBinding replaces the editable fields of an existing binding and
// bumps Version by 1. Per docs/prds/webhook-intake-security.md point 2:
// any edit always drops status to pending_approval, including edits to
// already-armed bindings -- an edit cannot silently re-arm, or the
// approval gate is decorative. Secret is preserved when the caller passes
// an empty string via COALESCE(NULLIF(?, ”), secret) so a partial update
// cannot erase the HMAC secret. Returns ErrBindingNotFound when no row
// matches, ErrBindingOverlap when the new source collides with a different
// binding.
func (s *Store) UpdateBinding(ctx context.Context, b binding.Binding) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var overlapID int64
	row := tx.QueryRowContext(ctx, `SELECT id FROM bindings WHERE source = ? AND id != ? LIMIT 1`,
		b.Matcher.Source, b.ID)
	if err := row.Scan(&overlapID); err == nil {
		return ErrBindingOverlap
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	now := time.Now().UTC().Format(bindingTimeLayout)
	secret, err := s.encryptBindingSecret(b.Secret)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE bindings SET name=?, source=?, mapping_id=?, workflow=?, secret=COALESCE(NULLIF(?, ''), secret),
			status = 'pending_approval',
			version = version + 1,
			updated_at = ?
		WHERE id=?`,
		b.Name, b.Matcher.Source, b.MappingID, b.Workflow, secret, now, b.ID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrBindingNotFound
	}
	return tx.Commit()
}

// DeleteBinding removes a binding by ID along with any rows it owns in
// binding_dispatches. The dedup rows are deleted in the same transaction
// so a recreate with the same source does not inherit old dedup state.
// Returns ErrBindingNotFound if it does not exist.
func (s *Store) DeleteBinding(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM binding_dispatches WHERE binding_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM bindings WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrBindingNotFound
	}
	return tx.Commit()
}

// ApproveBinding moves a binding from pending_approval to armed. The
// status guard is the SQL WHERE clause: a row whose status does not match
// pending_approval yields RowsAffected == 0 and is distinguished as a
// transition error (row exists, wrong from-status) or a not-found error
// (no row) by a follow-up SELECT inside the same transaction. Approve is
// also refused when a different binding is already armed for the same
// source: the overlap rule has to hold across status changes, so this is
// a TOCTOU guard rather than a primary check (Insert/Update own the
// initial overlap prevention).
func (s *Store) ApproveBinding(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var source string
	row := tx.QueryRowContext(ctx, `SELECT source FROM bindings WHERE id=?`, id)
	if err := row.Scan(&source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrBindingNotFound
		}
		return err
	}

	var otherArmed int64
	row = tx.QueryRowContext(ctx,
		`SELECT id FROM bindings WHERE source=? AND status='armed' AND id != ? LIMIT 1`,
		source, id)
	if err := row.Scan(&otherArmed); err == nil {
		return ErrBindingOverlap
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	now := time.Now().UTC().Format(bindingTimeLayout)
	res, err := tx.ExecContext(ctx, `
		UPDATE bindings SET status='armed', updated_at=?
		WHERE id=? AND status='pending_approval'`,
		now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrBindingTransition
	}
	return tx.Commit()
}

// ArmedBindingsForSource returns every armed binding for the given source,
// ordered by id so handleCapture picks a stable winner if multiple rows
// are ever present (the overlap guard should make this impossible in
// practice, but the store returns the full set rather than collapsing).
func (s *Store) ArmedBindingsForSource(ctx context.Context, source string) ([]binding.Binding, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, source, mapping_id, workflow, version, status, secret, created_at, updated_at
		FROM bindings WHERE source=? AND status='armed' ORDER BY id`, source)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []binding.Binding
	for rows.Next() {
		b, err := scanBindingRow(rows)
		if err != nil {
			return nil, err
		}
		if err := s.decryptBindingSecret(b); err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

// encryptBindingSecret seals a plaintext secret for persistence. With no
// cipher configured it returns the value unchanged (legacy plaintext).
func (s *Store) encryptBindingSecret(plaintext string) (string, error) {
	if s.bindingsCipher == nil {
		return plaintext, nil
	}
	return s.bindingsCipher.Encrypt(plaintext)
}

// decryptBindingSecret returns a binding's secret as plaintext. With no cipher
// configured the stored value is returned unchanged. A value that is not an
// encryption envelope is treated as a legacy plaintext row written before a
// cipher was configured and left readable as-is -- encryption applies to rows
// written or edited after the key is installed, not retroactively (see
// docs/prds/binding-secret-encryption.md).
func (s *Store) decryptBindingSecret(b *binding.Binding) error {
	if s.bindingsCipher == nil {
		return nil
	}
	if b.Secret == "" || !strings.HasPrefix(b.Secret, bindingEnvelopeMarker+":") {
		return nil
	}
	decrypted, err := s.bindingsCipher.Decrypt(b.Secret)
	if err != nil {
		return err
	}
	b.Secret = decrypted
	return nil
}

// sqlExecutor is the subset of *sql.DB / *sql.Tx that RecordDispatch needs.
// Both satisfy it; nil falls back to the store's own *sql.DB so callers
// that don't open an explicit transaction get best-effort semantics.
type sqlExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RecordDispatch writes a (binding, capture) dedup row. The caller may
// pass a *sql.Tx to make the dispatch row commit atomically with the
// surrounding task insert; pass nil to use the store's own connection
// (best-effort, not atomic with the task row). INSERT OR IGNORE +
// RowsAffected is the dedup test: a duplicate (binding_id, capture_id)
// is a no-op write and returns ErrAlreadyDispatched rather than a
// constraint error.
func (s *Store) RecordDispatch(
	ctx context.Context,
	tx *sql.Tx,
	bindingID int64,
	bindingVersion int64,
	captureID int64,
	taskID int64,
) error {
	exec := sqlExecutor(s.db)
	if tx != nil {
		exec = tx
	}
	res, err := exec.ExecContext(ctx, `
		INSERT OR IGNORE INTO binding_dispatches (binding_id, binding_version, capture_id, task_id, dispatched_at)
		VALUES (?, ?, ?, ?, ?)`,
		bindingID, bindingVersion, captureID, taskID,
		time.Now().UTC().Format(bindingTimeLayout))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrAlreadyDispatched
	}
	return nil
}

// ListUndispatchedCaptures returns authenticated captures that have not
// yet been dispatched by any armed binding whose source matches. The
// nested source query means the result is naturally limited to sources
// with an armed binding; an empty sources slice is therefore a no-arms
// short-circuit and returns (nil, nil) without hitting the table.
func (s *Store) ListUndispatchedCaptures(ctx context.Context, sources []string, limit int) ([]CapturedEvent, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, received_at, source, remote_addr, content_type, headers, body, authenticated
		FROM captured_events
		WHERE authenticated = 1
		  AND id NOT IN (SELECT capture_id FROM binding_dispatches)
		  AND source IN (
		    SELECT source FROM bindings WHERE status = 'armed' GROUP BY source
		  )
		ORDER BY id
		LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []CapturedEvent
	for rows.Next() {
		var c CapturedEvent
		var receivedAt string
		var authenticated int
		if err := rows.Scan(&c.ID, &receivedAt, &c.Source, &c.RemoteAddr,
			&c.ContentType, &c.Headers, &c.Body, &authenticated); err != nil {
			return nil, err
		}
		parsed, err := parseCaptureTime(receivedAt)
		if err != nil {
			return nil, err
		}
		c.ReceivedAt = parsed
		c.Authenticated = authenticated != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// bindingScanner is satisfied by both *sql.Row and *sql.Rows so
// scanBindingRow's column list stays in exactly one place.
type bindingScanner interface {
	Scan(dest ...any) error
}

func scanBindingRow(row bindingScanner) (*binding.Binding, error) {
	var b binding.Binding
	var createdAt, updatedAt string
	if err := row.Scan(&b.ID, &b.Name, &b.Matcher.Source, &b.MappingID, &b.Workflow,
		&b.Version, &b.Status, &b.Secret, &createdAt, &updatedAt); err != nil {
		return nil, err
	}
	created, err := time.Parse(bindingTimeLayout, createdAt)
	if err != nil {
		return nil, fmt.Errorf("store: unrecognised binding created_at %q: %w", createdAt, err)
	}
	updated, err := time.Parse(bindingTimeLayout, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("store: unrecognised binding updated_at %q: %w", updatedAt, err)
	}
	b.CreatedAt = created.UTC()
	b.UpdatedAt = updated.UTC()
	return &b, nil
}
