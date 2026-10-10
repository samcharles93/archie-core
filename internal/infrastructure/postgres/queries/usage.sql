-- name: InsertModelUsage :exec
-- A task's usage belongs to the task's org, whatever the caller says.
INSERT INTO model_usage (
	org_id, source, task_id, attempt, workflow, step, alias, provider, model,
	input_tokens, output_tokens, cached_tokens, at
) VALUES (
	COALESCE((SELECT t.org_id FROM tasks t WHERE t.id = sqlc.arg(task_id) AND sqlc.arg(task_id) <> 0), sqlc.arg(org_id)::text),
	sqlc.arg(source), sqlc.arg(task_id), sqlc.arg(attempt), sqlc.arg(workflow), sqlc.arg(step), sqlc.arg(alias),
	sqlc.arg(provider), sqlc.arg(model), sqlc.arg(input_tokens), sqlc.arg(output_tokens), sqlc.arg(cached_tokens), sqlc.arg(at)
);
