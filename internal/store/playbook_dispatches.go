package store

import (
	"context"
	"time"
)

// playbookDispatchesSchema is the durable idempotency ledger for
// side-effecting playbook actions (docs/prds/eda-playbook-engine.md gap 2).
// It copies binding_dispatches' conventions exactly: a durable table whose
// PRIMARY KEY is the structural (playbook_id, playbook_version, event_id,
// action_id) tuple, written with INSERT OR IGNORE so a duplicate is a no-op
// write rather than a constraint error.
const playbookDispatchesSchema = `
CREATE TABLE IF NOT EXISTS playbook_dispatches (
	playbook_id      TEXT NOT NULL,
	playbook_version TEXT NOT NULL,
	event_id         TEXT NOT NULL,
	action_id        TEXT NOT NULL,
	dispatched_at    TEXT NOT NULL,
	PRIMARY KEY (playbook_id, playbook_version, event_id, action_id)
);
`

// RecordPlaybookDispatch writes one (playbook, version, event, action) dedup
// row. INSERT OR IGNORE + RowsAffected is the dedup test: a duplicate is a
// no-op write and returns ErrAlreadyDispatched rather than a constraint
// error. The row is written and committed BEFORE the action's side effect is
// invoked by the coordinator, which is the deliberate reverse of
// binding_dispatches: an at-most-once ledger for non-revocable side effects.
func (s *Store) RecordPlaybookDispatch(
	ctx context.Context,
	playbookID string,
	playbookVersion string,
	eventID string,
	actionID string,
) error {
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO playbook_dispatches (playbook_id, playbook_version, event_id, action_id, dispatched_at)
		VALUES (?, ?, ?, ?, ?)`,
		playbookID, playbookVersion, eventID, actionID,
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

// DeletePlaybookDispatches removes every ledger row for the given playbook,
// mirroring DeleteBinding's row cleanup so a recreated playbook does not
// inherit stale dedup state.
func (s *Store) DeletePlaybookDispatches(ctx context.Context, playbookID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM playbook_dispatches WHERE playbook_id = ?`, playbookID)
	return err
}
