package postgres

import (
	"context"

	"github.com/samcharles93/archie-core/internal/domain/usage"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// RecordUsage appends one model call's usage record.
func (s *Store) RecordUsage(ctx context.Context, r usage.Record) error {
	return postgresdb.New(s.pool).InsertModelUsage(ctx, postgresdb.InsertModelUsageParams{
		TaskID: r.TaskID, OrgID: r.Org, Source: string(r.Source), Attempt: int64(r.Attempt),
		Workflow: r.Workflow, Step: r.Step, Alias: r.Alias, Provider: r.Provider, Model: r.Model,
		InputTokens: r.InputTokens, OutputTokens: r.OutputTokens, CachedTokens: r.CachedTokens, At: r.At,
	})
}
