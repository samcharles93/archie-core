-- The one-time legacy import's completion record.

-- name: InsertImportCompletion :exec
INSERT INTO import_completion (id, sources, report) VALUES (1, $1, $2);

-- name: ImportCompleted :one
SELECT EXISTS (SELECT 1 FROM import_completion);

-- name: InsertFreshInstallCompletion :exec
INSERT INTO import_completion (id, sources, report) VALUES (1, '', 'fresh install: no legacy data')
ON CONFLICT (id) DO NOTHING;
