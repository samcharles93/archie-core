-- Workflow inputs and bindings that assign them
-- (docs/prds/event-automation.md, "Workflows declare inputs" and "Bindings").
--
-- bindings.inputs is a JSON object assigning each workflow input either a
-- mapped parameter ({"param": "src_ip"}) or a constant ({"value": 3}).
-- bindings.repo_param names the mapped parameter holding "owner/name" when the
-- repository comes from the event rather than a fixed owner/repo.
-- tasks.inputs is the JSON object of input values a binding dispatch resolved;
-- empty for a task no binding started.

-- +goose Up

ALTER TABLE bindings ADD COLUMN inputs text NOT NULL DEFAULT '{}';
ALTER TABLE bindings ADD COLUMN repo_param text NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN inputs text NOT NULL DEFAULT '';

-- +goose Down

ALTER TABLE tasks DROP COLUMN inputs;
ALTER TABLE bindings DROP COLUMN repo_param;
ALTER TABLE bindings DROP COLUMN inputs;
