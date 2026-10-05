-- binding_dispatches.reason says why an evaluated capture did not start a
-- task; empty means it dispatched. Recording terminal non-dispatch outcomes
-- keeps the capture from being re-evaluated every cycle.

-- +goose Up

ALTER TABLE binding_dispatches ADD COLUMN reason text NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE binding_dispatches DROP COLUMN reason;
