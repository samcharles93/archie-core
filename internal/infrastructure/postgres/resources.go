package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
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

// ListResources supplies every org's documents for startup validation.
func (s *Resources) ListResources(ctx context.Context) ([]storecontract.Resource, error) {
	rows, err := postgresdb.New(s.pool).ListResources(ctx)
	if err != nil {
		return nil, err
	}
	resources := make([]storecontract.Resource, 0, len(rows))
	for _, row := range rows {
		resources = append(resources, resourceFromCurrent(row))
	}
	return resources, nil
}

func (s *Resources) Resource(ctx context.Context, orgID, kind string) (storecontract.Resource, error) {
	if err := checkResourceKey(orgID, kind); err != nil {
		return storecontract.Resource{}, err
	}
	resource, err := postgresdb.New(s.pool).ResourceByKind(ctx, postgresdb.ResourceByKindParams{OrgID: orgID, Kind: kind})
	if errors.Is(err, pgx.ErrNoRows) {
		return storecontract.Resource{}, storecontract.ErrResourceNotFound
	}
	if err != nil {
		return storecontract.Resource{}, err
	}
	return resourceFromCurrent(resource), nil
}

func (s *Resources) ResourceHistory(ctx context.Context, orgID, kind string, limit int) ([]storecontract.Resource, error) {
	if err := checkResourceKey(orgID, kind); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 0
	}
	history, err := postgresdb.New(s.pool).ResourceHistory(ctx, postgresdb.ResourceHistoryParams{
		OrgID: orgID, Kind: kind, EntryLimit: int64(limit),
	})
	if err != nil {
		return nil, err
	}
	resources := make([]storecontract.Resource, 0, len(history))
	for _, entry := range history {
		resources = append(resources, resourceFromAudit(
			entry.OrgID, entry.Kind, entry.Value, entry.Version, entry.Actor, entry.Source,
			entry.RequestID, entry.ExpectedVersion, entry.CurrentVersion, entry.At,
		))
	}
	return resources, nil
}

// checkResourceKey refuses a key the audit record key could not tell apart from
// another: an empty org or kind, or either containing the "/" that joins them
// in storecontract.ResourceAuditKey. A forgotten org fails here rather than
// reading as an absent resource.
func checkResourceKey(orgID, kind string) error {
	if orgID == "" || kind == "" || strings.Contains(orgID, "/") || strings.Contains(kind, "/") {
		return fmt.Errorf("invalid resource key: org %q, kind %q", orgID, kind)
	}
	return nil
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

// PutResource applies one control-plane resource write, idempotent per
// request ID.
func (s *Resources) PutResource(ctx context.Context, write storecontract.ResourceWrite) (_ storecontract.Resource, retErr error) {
	if err := checkResourceKey(write.OrgID, write.Kind); err != nil {
		return storecontract.Resource{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return storecontract.Resource{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	queries := postgresdb.New(tx)
	if err := queries.LockResourceWrite(ctx, postgresdb.LockResourceWriteParams{OrgID: write.OrgID, Kind: write.Kind}); err != nil {
		return storecontract.Resource{}, err
	}
	if existing, err := queries.ResourceByRequest(ctx, postgresdb.ResourceByRequestParams{
		OrgID: write.OrgID, Kind: write.Kind, RequestID: write.RequestID,
	}); err == nil {
		resource, err := s.replayedResource(ctx, queries, tx, existing)
		if err != nil {
			return storecontract.Resource{}, err
		}
		return resource, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return storecontract.Resource{}, err
	}

	current, err := queries.ResourceVersion(ctx, postgresdb.ResourceVersionParams{OrgID: write.OrgID, Kind: write.Kind})
	if errors.Is(err, pgx.ErrNoRows) {
		current = 0
	} else if err != nil {
		return storecontract.Resource{}, err
	}
	if current != write.ExpectedVersion {
		return storecontract.Resource{}, fmt.Errorf("%w: expected %d, current %d", storecontract.ErrResourceVersionConflict, write.ExpectedVersion, current)
	}
	history, err := appendResourceRevision(ctx, queries, write, current)
	if err != nil {
		return storecontract.Resource{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return storecontract.Resource{}, err
	}
	return resourceFromAudit(
		history.OrgID, history.Kind, history.Value, history.Version, history.Actor, history.Source,
		history.RequestID, history.ExpectedVersion, history.CurrentVersion, history.At,
	), nil
}

// appendResourceRevision writes the resource row, its ledger entry and its audit
// trail for one accepted write, returning the ledger entry. current is the
// version being replaced, zero for a first write.
func appendResourceRevision(ctx context.Context, queries *postgresdb.Queries, write storecontract.ResourceWrite, current int64) (postgresdb.InsertResourceHistoryRow, error) {
	if write.At.IsZero() {
		write.At = time.Now().UTC()
	} else {
		write.At = write.At.UTC()
	}

	var (
		resource postgresdb.Resource
		err      error
	)
	if current == 0 {
		resource, err = queries.InsertResource(ctx, postgresdb.InsertResourceParams{
			OrgID: write.OrgID, Kind: write.Kind, Value: write.Value, UpdatedAt: write.At,
		})
	} else {
		resource, err = queries.UpdateResource(ctx, postgresdb.UpdateResourceParams{
			OrgID: write.OrgID, Kind: write.Kind, Value: write.Value, UpdatedAt: write.At, Version: current,
		})
	}
	if err != nil {
		return postgresdb.InsertResourceHistoryRow{}, err
	}
	history, err := queries.InsertResourceHistory(ctx, postgresdb.InsertResourceHistoryParams{
		OrgID: write.OrgID, Kind: write.Kind, Value: write.Value, Version: resource.Version, Actor: write.Actor,
		Source: write.Source, RequestID: write.RequestID, ExpectedVersion: write.ExpectedVersion,
		CurrentVersion: current, At: write.At,
	})
	if err != nil {
		return postgresdb.InsertResourceHistoryRow{}, err
	}
	if err := queries.InsertResourceAudit(ctx, postgresdb.InsertResourceAuditParams{
		At: write.At, OrgID: write.OrgID, RecordKey: storecontract.ResourceAuditKey(write.OrgID, write.Kind), Kind: write.Kind, Version: resource.Version, Actor: write.Actor,
		Source: write.Source, RequestID: write.RequestID, PreviousVersion: current, Value: write.Value,
	}); err != nil {
		return postgresdb.InsertResourceHistoryRow{}, err
	}
	return history, nil
}

// replayedResource answers a repeated request ID with its recorded revision,
// re-creating the row if it was removed since.
func (s *Resources) replayedResource(ctx context.Context, queries *postgresdb.Queries, tx pgx.Tx, existing postgresdb.ResourceByRequestRow) (storecontract.Resource, error) {
	if _, err := queries.ResourceByKind(ctx, postgresdb.ResourceByKindParams{OrgID: existing.OrgID, Kind: existing.Kind}); errors.Is(err, pgx.ErrNoRows) {
		if _, err := queries.ReinsertResource(ctx, postgresdb.ReinsertResourceParams{
			OrgID: existing.OrgID, Kind: existing.Kind, Value: existing.Value, Version: existing.Version, UpdatedAt: existing.At,
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
		existing.OrgID, existing.Kind, existing.Value, existing.Version, existing.Actor, existing.Source,
		existing.RequestID, existing.ExpectedVersion, existing.CurrentVersion, existing.At,
	), nil
}

func resourceFromCurrent(resource postgresdb.Resource) storecontract.Resource {
	return storecontract.Resource{
		OrgID: resource.OrgID, Kind: resource.Kind, Value: resource.Value, Version: resource.Version, At: resource.UpdatedAt,
	}
}

func resourceFromAudit(
	orgID, kind string,
	value []byte,
	version int64,
	actor, source, requestID string,
	expectedVersion, currentVersion int64,
	at time.Time,
) storecontract.Resource {
	return storecontract.Resource{
		OrgID: orgID, Kind: kind, Value: value, Version: version, Actor: actor, Source: source,
		RequestID: requestID, ExpectedVersion: expectedVersion, CurrentVersion: currentVersion, At: at,
	}
}
