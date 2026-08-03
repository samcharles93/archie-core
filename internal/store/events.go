package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/events"
)

const eventsSchema = `
CREATE TABLE IF NOT EXISTS events (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	at        TEXT NOT NULL,
	kind      TEXT NOT NULL,
	task_id   INTEGER NOT NULL DEFAULT 0,
	repo      TEXT NOT NULL DEFAULT '',
	issue     INTEGER NOT NULL DEFAULT 0,
	workflow  TEXT NOT NULL DEFAULT '',
	stage     TEXT NOT NULL DEFAULT '',
	detail    TEXT NOT NULL DEFAULT '',
	data      TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS idx_events_task ON events(task_id, id);
CREATE INDEX IF NOT EXISTS idx_events_kind ON events(kind, id);
`

// InsertEvent appends an event to the log and returns its row id.
func (s *Store) InsertEvent(ctx context.Context, e events.Event) (int64, error) {
	data, err := json.Marshal(e.Data)
	if err != nil {
		data = fmt.Appendf(nil, `{"marshal_error":%q}`, err.Error())
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO events (at, kind, task_id, repo, issue, workflow, stage, detail, data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.At.UTC().Format(time.RFC3339Nano), e.Kind, e.TaskID, e.Repo, e.Issue,
		e.Workflow, e.Stage, clip(e.Detail, 4000), string(data))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func scanEvents(rows *sql.Rows) (out []events.Event, retErr error) {
	defer func() {
		retErr = errors.Join(retErr, rows.Close())
	}()
	for rows.Next() {
		var e events.Event
		var at, data string
		if err := rows.Scan(&e.ID, &at, &e.Kind, &e.TaskID, &e.Repo, &e.Issue,
			&e.Workflow, &e.Stage, &e.Detail, &data); err != nil {
			return nil, err
		}
		e.At, _ = time.Parse(time.RFC3339Nano, at)
		_ = json.Unmarshal([]byte(data), &e.Data)
		out = append(out, e)
	}
	return out, rows.Err()
}

const eventCols = "id, at, kind, task_id, repo, issue, workflow, stage, detail, data"

// EventsSince returns up to limit events with id > sinceID, oldest
// first  --  SSE catch-up and the live feed.
func (s *Store) EventsSince(ctx context.Context, sinceID int64, limit int) ([]events.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventCols+` FROM events WHERE id > ? ORDER BY id LIMIT ?`, sinceID, limit)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// RecentEvents returns the newest limit events, newest first.
func (s *Store) RecentEvents(ctx context.Context, limit int) ([]events.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventCols+` FROM events ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// TaskEvents returns a task's full timeline, oldest first.
func (s *Store) TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+eventCols+` FROM events WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// Tasks returns all tasks, newest first (dashboard listing).
func (s *Store) Tasks(ctx context.Context, limit int) (tasks []Task, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, owner, repo, issue_number, title, status, workflow, stage,
			pr_number, tokens_used, iterations, attempt, park_reason, retry_count,
			created_at, updated_at
		FROM tasks ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, rows.Close())
	}()
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.Owner, &t.Repo, &t.IssueNumber, &t.Title,
			&t.Status, &t.Workflow, &t.Stage, &t.PRNumber, &t.TokensUsed,
			&t.Iterations, &t.Attempt, &t.ParkReason, &t.RetryCount,
			sqliteTime{&t.CreatedAt}, sqliteTime{&t.UpdatedAt}); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

// StatusCounts returns task counts by status.
func (s *Store) StatusCounts(ctx context.Context) (counts map[string]int, retErr error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM tasks GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, rows.Close())
	}()
	counts = map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		counts[st] = n
	}
	return counts, rows.Err()
}

// WorkflowStat is one row of the per-workflow metrics table.
type WorkflowStat struct {
	Workflow   string  `json:"workflow"`
	Runs       int     `json:"runs"`
	Merged     int     `json:"merged"`
	PROpen     int     `json:"pr_open"`
	Parked     int     `json:"parked"`
	AvgTokens  int     `json:"avg_tokens"`
	AvgSteps   float64 `json:"avg_steps"`
	TotalToken int     `json:"total_tokens"`
}

// WorkflowStats aggregates outcomes and spend per workflow  --  the
// core "is archie getting better per dollar" table.
func (s *Store) WorkflowStats(ctx context.Context) (stats []WorkflowStat, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT workflow, COUNT(*),
			SUM(status='merged'), SUM(status='pr_open'), SUM(status='parked'),
			CAST(AVG(tokens_used) AS INTEGER), AVG(iterations), SUM(tokens_used)
		FROM tasks WHERE workflow != '' GROUP BY workflow ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, rows.Close())
	}()
	for rows.Next() {
		var w WorkflowStat
		if err := rows.Scan(&w.Workflow, &w.Runs, &w.Merged, &w.PROpen, &w.Parked,
			&w.AvgTokens, &w.AvgSteps, &w.TotalToken); err != nil {
			return nil, err
		}
		stats = append(stats, w)
	}
	return stats, rows.Err()
}

// StageStat is average stage duration and failure counts per stage.
type StageStat struct {
	Workflow string `json:"workflow"`
	Stage    string `json:"stage"`
	Runs     int    `json:"runs"`
	AvgMs    int    `json:"avg_ms"`
	Errors   int    `json:"errors"`
}

// StageStats aggregates stage_finish events  --  where does time go, and
// which stages fail.
func (s *Store) StageStats(ctx context.Context) (stats []StageStat, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT workflow, stage, COUNT(*),
			CAST(AVG(json_extract(data,'$.duration_ms')) AS INTEGER),
			SUM(CASE WHEN json_extract(data,'$.error') IS NOT NULL THEN 1 ELSE 0 END)
		FROM events WHERE kind = 'stage_finish'
		GROUP BY workflow, stage ORDER BY workflow, stage`)
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, rows.Close())
	}()
	for rows.Next() {
		var st StageStat
		var avg sql.NullInt64
		if err := rows.Scan(&st.Workflow, &st.Stage, &st.Runs, &avg, &st.Errors); err != nil {
			return nil, err
		}
		st.AvgMs = int(avg.Int64)
		stats = append(stats, st)
	}
	return stats, rows.Err()
}

// DayTokens is token spend per UTC day.
type DayTokens struct {
	Day    string `json:"day"`
	Tokens int    `json:"tokens"`
}

// TokensByDay sums agent token spend per day from agent_finish events.
func (s *Store) TokensByDay(ctx context.Context, days int) (tokens []DayTokens, retErr error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT substr(at,1,10) AS day,
			CAST(SUM(json_extract(data,'$.tokens')) AS INTEGER)
		FROM events WHERE kind = 'agent_finish'
		GROUP BY day ORDER BY day DESC LIMIT ?`, days)
	if err != nil {
		return nil, err
	}
	defer func() {
		retErr = errors.Join(retErr, rows.Close())
	}()
	for rows.Next() {
		var d DayTokens
		if err := rows.Scan(&d.Day, &d.Tokens); err != nil {
			return nil, err
		}
		tokens = append(tokens, d)
	}
	return tokens, rows.Err()
}
