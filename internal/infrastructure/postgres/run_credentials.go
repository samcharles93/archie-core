package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

var _ storecontract.RunCredentialStore = (*Store)(nil)

func (s *Store) PutRunCredential(ctx context.Context, digest [32]byte, taskID int64, expires time.Time) error {
	return s.queries().InsertRunCredential(ctx, postgresdb.InsertRunCredentialParams{Digest: digest[:], TaskID: taskID, ExpiresAt: expires})
}

func (s *Store) DeleteRunCredential(ctx context.Context, digest [32]byte) error {
	return s.queries().DeleteRunCredential(ctx, digest[:])
}

func (s *Store) RunCredentialTask(ctx context.Context, digest [32]byte, now time.Time) (int64, error) {
	id, err := s.queries().RunCredentialTask(ctx, postgresdb.RunCredentialTaskParams{Digest: digest[:], ExpiresAt: now})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, storecontract.ErrRunCredentialUnknown
	}
	return id, err
}

func (s *Store) DeleteExpiredRunCredentials(ctx context.Context, now time.Time) error {
	return s.queries().DeleteExpiredRunCredentials(ctx, now)
}
