package archied

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// runPostgresRecovery performs one recovery operation on the PostgreSQL
// database the configuration names. There is no automatic schema rollback:
// goose Down drops tables, so the way back from a migration is restoring the
// snapshot taken before it, which loses every write made after that snapshot.
func runPostgresRecovery(ctx context.Context, options StateStoreRecoveryOptions) (string, error) {
	cfg, err := bootConfig(ctx, options)
	if err != nil {
		return "", err
	}
	url := cfg.DatabaseURL
	if url == "" {
		return "", errors.New("database_url is required: the configuration names no PostgreSQL database (pass -db for a legacy SQLite task file)")
	}
	switch options.Operation {
	case RecoveryBackup:
		if options.Out == "" {
			return "", errors.New("backup requires -out")
		}
		if err := postgres.Backup(ctx, url, options.Out); err != nil {
			return "", err
		}
		return "backed up the whole database to " + options.Out, nil
	case RecoveryRestore:
		if options.From == "" {
			return "", errors.New("restore requires -from")
		}
		if err := postgres.Restore(ctx, url, options.From); err != nil {
			return "", err
		}
		return fmt.Sprintf("restored the whole database from %s; every write made after the snapshot is gone; start the archie services again", options.From), nil
	case RecoveryValidate:
		return validatePostgres(ctx, url, options)
	case RecoveryRollback:
		if options.Kind == "" {
			return "", errors.New("rollback requires -kind")
		}
		return rollbackPostgres(ctx, url, options)
	default:
		return "", fmt.Errorf("unknown recovery command %q", options.Operation)
	}
}

// openRecoveryPool opens the database without migrating it: recovery inspects
// or repairs the schema it finds, and refuses one newer than this binary.
func openRecoveryPool(ctx context.Context, url string) (*pgxpool.Pool, int64, error) {
	pool, err := postgres.Open(ctx, url)
	if err != nil {
		return nil, 0, err
	}
	version, err := postgres.SchemaVersion(ctx, pool)
	if err == nil {
		err = postgres.CheckSchemaVersion(version)
	}
	if err != nil {
		pool.Close()
		return nil, 0, err
	}
	return pool, version, nil
}

// validatePostgres is validateStore for the PostgreSQL database. An
// unmigrated database holds nothing to check; the State Store migrates and
// seeds it on its next start, so only boot's config gate applies.
func validatePostgres(ctx context.Context, url string, options StateStoreRecoveryOptions) (string, error) {
	pool, version, err := openRecoveryPool(ctx, url)
	if err != nil {
		return "", err
	}
	defer pool.Close()
	server, err := openStateStoreControlPlane(postgres.NewResources(pool))
	if err != nil {
		return "", err
	}
	checked, err := checkBootGate(ctx, server, version > 0, options)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("the database is valid at schema version %d; %d stored resources validate", version, checked), nil
}

// rollbackPostgres replays an earlier revision holding the State Store's
// ownership claim, so it refuses while a State Store serves and none can
// start until it finishes.
func rollbackPostgres(ctx context.Context, url string, options StateStoreRecoveryOptions) (string, error) {
	pool, _, err := openRecoveryPool(ctx, url)
	if err != nil {
		return "", err
	}
	defer pool.Close()
	claim, err := postgres.AcquireOwnership(ctx, pool, postgres.OwnerStateStore)
	if err != nil {
		return "", fmt.Errorf("rollback refused, stop the State Store first: %w", err)
	}
	defer func() { _ = claim.Release(context.WithoutCancel(ctx)) }()
	return replayRevision(ctx, postgres.NewResources(pool), options)
}
