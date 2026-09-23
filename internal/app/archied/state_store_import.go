package archied

import (
	"context"
	"errors"
	"fmt"

	"github.com/samcharles93/archie-core/internal/infrastructure/legacyimport"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres"
)

// legacySources names an install's three legacy SQLite files from its
// configured db_path, the same derivation the legacy openers use, so an
// operator cannot import one store and forget another.
func legacySources(dbPath string) legacyimport.Sources {
	return legacyimport.Sources{
		StateStore: taskDBPath(dbPath),
		EDA:        edaDBPath(dbPath),
		Gateway:    conversationDBPath(dbPath),
	}
}

// importLegacy runs the one-time import of the legacy files into Postgres.
// It is an offline command of the State Store binary, not of archied: offline
// maintenance of a store belongs to the process that owns it.
func importLegacy(ctx context.Context, options StateStoreRecoveryOptions) (string, error) {
	dbPath, url := options.DBPath, options.DatabaseURL
	if dbPath == "" || url == "" {
		cfg, err := bootConfig(ctx, options)
		if err != nil {
			return "", err
		}
		if dbPath == "" {
			dbPath = cfg.DBPath
		}
		if url == "" {
			url = cfg.DatabaseURL
		}
	}
	if dbPath == "" {
		return "", errors.New("import requires db_path (-db-path or the configuration)")
	}
	if url == "" {
		return "", errors.New("import requires database_url (-database-url or the configuration)")
	}
	pool, err := postgres.Open(ctx, url)
	if err != nil {
		return "", err
	}
	defer pool.Close()
	report, err := legacyimport.Import(ctx, pool, legacySources(dbPath))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("imported %s into Postgres and recorded the completion: %s", dbPath, report), nil
}
