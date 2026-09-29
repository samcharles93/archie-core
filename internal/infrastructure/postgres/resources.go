package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

// Resources is the PostgreSQL implementation of the State Store's
// control-plane resource document operations.
type Resources struct {
	pool *pgxpool.Pool
}

func NewResources(pool *pgxpool.Pool) *Resources {
	return &Resources{pool: pool}
}

func (s *Resources) Resource(ctx context.Context, kind string) (storecontract.Resource, error) {
	resource, err := postgresdb.New(s.pool).ResourceByKind(ctx, kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return storecontract.Resource{}, storecontract.ErrResourceNotFound
	}
	if err != nil {
		return storecontract.Resource{}, err
	}
	return resourceFromCurrent(resource), nil
}

func (s *Resources) ResourceHistory(ctx context.Context, kind string, limit int) ([]storecontract.Resource, error) {
	if limit <= 0 {
		limit = 0
	}
	history, err := postgresdb.New(s.pool).ResourceHistory(ctx, postgresdb.ResourceHistoryParams{
		Kind: kind, EntryLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	resources := make([]storecontract.Resource, 0, len(history))
	for _, entry := range history {
		resources = append(resources, resourceFromAudit(
			entry.Kind, entry.Value, entry.Version, entry.Actor, entry.Source,
			entry.RequestID, entry.ExpectedVersion, entry.CurrentVersion, entry.At,
		))
	}
	return resources, nil
}

// Audit returns the field-level audit of the named records of one table,
// newest first. A limit of zero or less returns every entry.
func (s *Resources) Audit(ctx context.Context, table string, keys []string, limit int) ([]storecontract.AuditEntry, error) {
	if limit <= 0 {
		limit = math.MaxInt32
	}
	rows, err := postgresdb.New(s.pool).AuditForRecords(ctx, postgresdb.AuditForRecordsParams{
		TableName: table, RecordKeys: keys, EntryLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	entries := make([]storecontract.AuditEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, storecontract.AuditEntry{
			ID: row.ID, Table: row.TableName, RecordKey: row.RecordKey, Field: row.Field,
			OldValue: row.OldValue, NewValue: row.NewValue, Version: row.RecordVersion,
			Actor: row.Actor, Source: row.Source, RequestID: row.RequestID, At: row.At,
		})
	}
	return entries, nil
}

// PutResource applies one control-plane resource write.
//
// It is idempotent per request ID: a write whose (kind, request_id) is already
// in resource_history returns that revision instead of writing a second one, so a
// retry that reached the store twice is still one revision. The ledger is what
// carries the key -- resource_history outlives the row it describes -- so a
// replay of a revision whose resource an operator removed re-creates that row
// rather than answering with a revision the store no longer holds. The
// alternative is a write that reports a version for a kind that is absent: the
// caller cannot tell the difference, and a kind can stay gone while every
// process believes it holds a value.
func (s *Resources) PutResource(ctx context.Context, write storecontract.ResourceWrite) (_ storecontract.Resource, retErr error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storecontract.Resource{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := postgresdb.New(tx)
	if err := queries.LockResourceWrite(ctx, write.Kind); err != nil {
		return storecontract.Resource{}, err
	}
	if existing, err := queries.ResourceByRequest(ctx, postgresdb.ResourceByRequestParams{
		Kind: write.Kind, RequestID: write.RequestID,
	}); err == nil {
		resource, err := s.replayedResource(ctx, queries, tx, existing)
		if err != nil {
			return storecontract.Resource{}, err
		}
		return resource, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return storecontract.Resource{}, err
	}

	current, err := queries.ResourceVersion(ctx, write.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		current = 0
	} else if err != nil {
		return storecontract.Resource{}, err
	}
	if current != write.ExpectedVersion {
		return storecontract.Resource{}, fmt.Errorf("%w: expected %d, current %d", storecontract.ErrResourceVersionConflict, write.ExpectedVersion, current)
	}
	if write.At.IsZero() {
		write.At = time.Now().UTC()
	} else {
		write.At = write.At.UTC()
	}

	var resource postgresdb.Resource
	if current == 0 {
		resource, err = queries.InsertResource(ctx, postgresdb.InsertResourceParams{
			Kind: write.Kind, Value: write.Value, UpdatedAt: write.At,
		})
	} else {
		resource, err = queries.UpdateResource(ctx, postgresdb.UpdateResourceParams{
			Kind: write.Kind, Value: write.Value, UpdatedAt: write.At, Version: current,
		})
	}
	if err != nil {
		return storecontract.Resource{}, err
	}
	history, err := queries.InsertResourceHistory(ctx, postgresdb.InsertResourceHistoryParams{
		Kind: write.Kind, Value: write.Value, Version: resource.Version, Actor: write.Actor,
		Source: write.Source, RequestID: write.RequestID, ExpectedVersion: write.ExpectedVersion,
		CurrentVersion: current, At: write.At,
	})
	if err != nil {
		return storecontract.Resource{}, err
	}
	if err := queries.InsertResourceAudit(ctx, postgresdb.InsertResourceAuditParams{
		At: write.At, Kind: write.Kind, Version: resource.Version, Actor: write.Actor,
		Source: write.Source, RequestID: write.RequestID, PreviousVersion: current, Value: write.Value,
	}); err != nil {
		return storecontract.Resource{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return storecontract.Resource{}, err
	}
	return resourceFromAudit(
		history.Kind, history.Value, history.Version, history.Actor, history.Source,
		history.RequestID, history.ExpectedVersion, history.CurrentVersion, history.At,
	), nil
}

// replayedResource answers a write whose request ID resource_history already
// holds. The ledger's revision is the answer and a replay writes nothing, with
// one repair: when the resource that revision describes is no longer the current
// row, the row is re-created from it. That is the state an operator removing a
// resource leaves behind, and without the repair the store answers with a
// revision it does not hold, so the caller reads a version for a kind that is
// absent and the kind stays absent.
func (s *Resources) replayedResource(ctx context.Context, queries *postgresdb.Queries, tx pgx.Tx, existing postgresdb.ResourceByRequestRow) (storecontract.Resource, error) {
	if _, err := queries.ResourceByKind(ctx, existing.Kind); errors.Is(err, pgx.ErrNoRows) {
		if _, err := queries.ReinsertResource(ctx, postgresdb.ReinsertResourceParams{
			Kind: existing.Kind, Value: existing.Value, Version: existing.Version, UpdatedAt: existing.At,
		}); err != nil {
			return storecontract.Resource{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return storecontract.Resource{}, err
		}
	} else if err != nil {
		return storecontract.Resource{}, err
	}
	return resourceFromAudit(
		existing.Kind, existing.Value, existing.Version, existing.Actor, existing.Source,
		existing.RequestID, existing.ExpectedVersion, existing.CurrentVersion, existing.At,
	), nil
}

func resourceFromCurrent(resource postgresdb.Resource) storecontract.Resource {
	return storecontract.Resource{
		Kind: resource.Kind, Value: resource.Value, Version: resource.Version, At: resource.UpdatedAt,
	}
}

func resourceFromAudit(
	kind string,
	value []byte,
	version int64,
	actor, source, requestID string,
	expectedVersion, currentVersion int64,
	at time.Time,
) storecontract.Resource {
	return storecontract.Resource{
		Kind: kind, Value: value, Version: version, Actor: actor, Source: source,
		RequestID: requestID, ExpectedVersion: expectedVersion, CurrentVersion: currentVersion, At: at,
	}
}
