package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// insertEventQ writes one event through q (a pool- or tx-scoped Queries value)
// and returns its row id. It reproduces the SQLite insertEvent's write-side
// semantics: a zero At becomes now-UTC, an unmarshallable Data becomes the
// marshal_error envelope, and Detail is clipped to 4000 bytes.
func insertEventQ(ctx context.Context, q *postgresdb.Queries, e events.Event) (int64, error) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	data, err := json.Marshal(e.Data)
	if err != nil {
		data = fmt.Appendf(nil, `{"marshal_error":%q}`, err.Error())
	}
	return q.InsertEvent(ctx, postgresdb.InsertEventParams{
		At: e.At.UTC(), Kind: e.Kind, TaskID: e.TaskID, Repo: e.Repo,
		Issue: int64(e.Issue), Workflow: e.Workflow, Stage: e.Stage,
		Attempt: int64(e.Attempt), ActorID: e.ActorID, ActorKind: e.ActorKind,
		PrincipalID: e.PrincipalID, Detail: clip(e.Detail, 4000), Data: string(data),
	})
}

// InsertEvent appends an event to the log and returns its row id.
func (s *Store) InsertEvent(ctx context.Context, e events.Event) (int64, error) {
	return insertEventQ(ctx, s.queries(), e)
}

// eventFromRow maps the generated event row to the domain event, decoding the
// stored JSON data document back into a map.
func eventFromRow(e postgresdb.Event) events.Event {
	var data map[string]any
	_ = json.Unmarshal([]byte(e.Data), &data)
	return events.Event{
		ID: e.ID, At: e.At.UTC(), Kind: e.Kind, TaskID: e.TaskID,
		Repo: e.Repo, Issue: int(e.Issue), Workflow: e.Workflow, Stage: e.Stage,
		Attempt: int(e.Attempt), ActorID: e.ActorID, ActorKind: e.ActorKind,
		PrincipalID: e.PrincipalID, Detail: e.Detail, Data: data,
	}
}

// EventsSince returns up to limit events after the opaque cursor, oldest
// first. The cursor carries the total order (at, id): an empty or malformed
// cursor means "from the beginning".
func (s *Store) EventsSince(ctx context.Context, cursor string, limit int) ([]events.Event, error) {
	at, id, ok := storecontract.ParseEventCursor(cursor)
	if !ok {
		return s.listEventsFromBeginning(ctx, limit)
	}
	atTime, err := time.Parse(storecontract.EventCursorLayout, at)
	if err != nil {
		// A malformed sort key means "from the beginning".
		return s.listEventsFromBeginning(ctx, limit)
	}
	rows, err := s.queries().ListEventsAfter(ctx, postgresdb.ListEventsAfterParams{
		At: atTime, ID: id, Limit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	return eventsFromRows(rows), nil
}

func (s *Store) listEventsFromBeginning(ctx context.Context, limit int) ([]events.Event, error) {
	rows, err := s.queries().ListEventsFromBeginning(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	return eventsFromRows(rows), nil
}

func eventsFromRows(rows []postgresdb.Event) []events.Event {
	out := make([]events.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, eventFromRow(r))
	}
	return out
}

// TaskEvents returns a task's full timeline, oldest first.
func (s *Store) TaskEvents(ctx context.Context, taskID int64) ([]events.Event, error) {
	rows, err := s.queries().TaskEventsByID(ctx, taskID)
	if err != nil {
		return nil, err
	}
	return eventsFromRows(rows), nil
}

// WorkflowStats aggregates outcomes and spend per workflow.
func (s *Store) WorkflowStats(ctx context.Context) ([]storecontract.WorkflowStat, error) {
	rows, err := s.queries().WorkflowStats(ctx)
	if err != nil {
		return nil, err
	}
	stats := make([]storecontract.WorkflowStat, 0, len(rows))
	for _, r := range rows {
		stats = append(stats, storecontract.WorkflowStat{
			Workflow:   r.Workflow,
			Runs:       int(r.Runs),
			Merged:     int(r.Merged),
			Completed:  int(r.Completed),
			PROpen:     int(r.PrOpen),
			Parked:     int(r.Parked),
			AvgTokens:  int(r.AvgTokens),
			AvgSteps:   r.AvgSteps,
			TotalToken: int(r.TotalTokens),
		})
	}
	return stats, nil
}

// StageStats aggregates stage_finish events -- where time goes, and which
// stages fail.
func (s *Store) StageStats(ctx context.Context) ([]storecontract.StageStat, error) {
	rows, err := s.queries().StageStats(ctx)
	if err != nil {
		return nil, err
	}
	stats := make([]storecontract.StageStat, 0, len(rows))
	for _, r := range rows {
		stats = append(stats, storecontract.StageStat{
			Workflow: r.Workflow, Stage: r.Stage, Runs: int(r.Runs),
			AvgMs: int(r.AvgMs), Errors: int(r.Errors),
		})
	}
	return stats, nil
}

// TokensByDay sums task token spend per UTC day, using agent_finish events as
// the per-run accounting source and tasks.tokens_used as the durable fallback.
func (s *Store) TokensByDay(ctx context.Context, days int) ([]storecontract.DayTokens, error) {
	rows, err := s.queries().TokensByDay(ctx, int32(days))
	if err != nil {
		return nil, err
	}
	tokens := make([]storecontract.DayTokens, 0, len(rows))
	for _, r := range rows {
		tokens = append(tokens, storecontract.DayTokens{Day: r.Day, Tokens: int(r.Tokens)})
	}
	return tokens, nil
}
