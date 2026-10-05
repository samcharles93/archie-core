-- captures.delivery names the sender's delivery: its delivery ID header when
-- it sends one, else the capture itself. A retry of one delivery shares it, so
-- binding_dispatches keyed on it dispatches each delivery once however many
-- times it arrives.

-- +goose Up

ALTER TABLE captures ADD COLUMN delivery text NOT NULL DEFAULT '';
UPDATE captures SET delivery = id;

ALTER TABLE binding_dispatches ADD COLUMN delivery text NOT NULL DEFAULT '';
UPDATE binding_dispatches SET delivery = capture;
DROP INDEX idx_binding_dispatch_once;
CREATE UNIQUE INDEX idx_binding_dispatch_once ON binding_dispatches (binding, delivery);

-- +goose Down

DROP INDEX idx_binding_dispatch_once;
CREATE UNIQUE INDEX idx_binding_dispatch_once ON binding_dispatches (binding, capture);
ALTER TABLE binding_dispatches DROP COLUMN delivery;
ALTER TABLE captures DROP COLUMN delivery;
