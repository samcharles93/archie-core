package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
	"github.com/samcharles93/archie-core/internal/store"
)

// Resources is the PostgreSQL implementation of the State Store's
// control-plane resource document operations. It is separate while the rest
// of the transitional SQLite Store is still being ported.
type Resources struct {
	pool *pgxpool.Pool
}

func NewResources(pool *pgxpool.Pool) *Resources {
	return &Resources{pool: pool}
}

func (s *Resources) Resource(ctx context.Context, kind string) (store.Resource, error) {
	resource, err := postgresdb.New(s.pool).ResourceByKind(ctx, kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Resource{}, store.ErrResourceNotFound
	}
	if err != nil {
		return store.Resource{}, err
	}
	return resourceFromCurrent(resource), nil
}

func (s *Resources) ResourceHistory(ctx context.Context, kind string, limit int) ([]store.Resource, error) {
	if limit <= 0 {
		limit = 0
	}
	history, err := postgresdb.New(s.pool).ResourceHistory(ctx, postgresdb.ResourceHistoryParams{
		Kind: kind, EntryLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	resources := make([]store.Resource, 0, len(history))
	for _, entry := range history {
		resources = append(resources, resourceFromAudit(
			entry.Kind, entry.Value, entry.Version, entry.Actor, entry.Source,
			entry.RequestID, entry.ExpectedVersion, entry.CurrentVersion, entry.At,
		))
	}
	return resources, nil
}

func (s *Resources) PutResource(ctx context.Context, write store.ResourceWrite) (_ store.Resource, retErr error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Resource{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := postgresdb.New(tx)
	if err := queries.LockResourceWrite(ctx, write.Kind); err != nil {
		return store.Resource{}, err
	}
	if existing, err := queries.ResourceByRequest(ctx, postgresdb.ResourceByRequestParams{
		Kind: write.Kind, RequestID: write.RequestID,
	}); err == nil {
		return resourceFromAudit(
			existing.Kind, existing.Value, existing.Version, existing.Actor, existing.Source,
			existing.RequestID, existing.ExpectedVersion, existing.CurrentVersion, existing.At,
		), nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return store.Resource{}, err
	}

	current, err := queries.ResourceVersion(ctx, write.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		current = 0
	} else if err != nil {
		return store.Resource{}, err
	}
	if current != write.ExpectedVersion {
		return store.Resource{}, fmt.Errorf("%w: expected %d, current %d", store.ErrResourceVersionConflict, write.ExpectedVersion, current)
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
		return store.Resource{}, err
	}
	history, err := queries.InsertResourceHistory(ctx, postgresdb.InsertResourceHistoryParams{
		Kind: write.Kind, Value: write.Value, Version: resource.Version, Actor: write.Actor,
		Source: write.Source, RequestID: write.RequestID, ExpectedVersion: write.ExpectedVersion,
		CurrentVersion: current, At: write.At,
	})
	if err != nil {
		return store.Resource{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Resource{}, err
	}
	return resourceFromAudit(
		history.Kind, history.Value, history.Version, history.Actor, history.Source,
		history.RequestID, history.ExpectedVersion, history.CurrentVersion, history.At,
	), nil
}

func resourceFromCurrent(resource postgresdb.Resource) store.Resource {
	return store.Resource{
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
) store.Resource {
	return store.Resource{
		Kind: kind, Value: value, Version: version, Actor: actor, Source: source,
		RequestID: requestID, ExpectedVersion: expectedVersion, CurrentVersion: currentVersion, At: at,
	}
}
