-- Rows written before their writers stamped an org took the column default,
-- the system org. Each takes the org of the record it belongs to: a task's
-- events, transitions, steps and tool calls the task's, and a source's event
-- types, bindings and mappings the source's.
-- +goose Up
UPDATE events e SET org_id = t.org_id, workspace_id = t.workspace_id
FROM tasks t WHERE e.task_id = t.id AND e.org_id <> t.org_id;
UPDATE transitions x SET org_id = t.org_id, workspace_id = t.workspace_id
FROM tasks t WHERE x.task_id = t.id AND x.org_id <> t.org_id;
UPDATE step_executions x SET org_id = t.org_id, workspace_id = t.workspace_id
FROM tasks t WHERE x.execution_id = t.id AND x.org_id <> t.org_id;
UPDATE tool_calls x SET org_id = t.org_id, workspace_id = t.workspace_id
FROM tasks t WHERE x.task_id = t.id AND x.org_id <> t.org_id;
UPDATE event_types x SET org_id = s.org_id, workspace_id = s.workspace_id
FROM sources s WHERE x.source = s.path AND x.org_id <> s.org_id;
UPDATE bindings x SET org_id = s.org_id, workspace_id = s.workspace_id
FROM sources s WHERE x.source = s.path AND x.org_id <> s.org_id;
UPDATE mappings x SET org_id = y.org_id, workspace_id = y.workspace_id
FROM event_types y WHERE x.event_type = y.id AND x.org_id <> y.org_id;

-- +goose Down
SELECT 1;
