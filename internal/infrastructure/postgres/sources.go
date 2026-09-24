package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/domain/source"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

var _ storecontract.SourceStore = (*EDA)(nil)

// InsertSource stores a new source. The path is the primary key, so a taken
// path is refused by the database and surfaced as ErrSourcePathTaken.
func (s *EDA) InsertSource(ctx context.Context, src source.Source) error {
	secret, err := s.sealSecret(src.Secret)
	if err != nil {
		return err
	}
	err = s.q.InsertSource(ctx, postgresdb.InsertSourceParams{
		Path: src.Path, Signing: string(src.Signing), Secret: secret,
	})
	if isUniqueViolation(err) {
		return storecontract.ErrSourcePathTaken
	}
	if err != nil {
		return fmt.Errorf("edastore: insert source: %w", err)
	}
	return nil
}

// GetSource returns (nil, nil) for an unknown path.
func (s *EDA) GetSource(ctx context.Context, path string) (*source.Source, error) {
	r, err := s.q.GetSource(ctx, path)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("edastore: get source: %w", err)
	}
	src, err := s.sourceValue(r)
	if err != nil {
		return nil, err
	}
	return &src, nil
}

func (s *EDA) ListSources(ctx context.Context) ([]source.Source, error) {
	rows, err := s.q.ListSources(ctx)
	if err != nil {
		return nil, fmt.Errorf("edastore: list sources: %w", err)
	}
	out := make([]source.Source, 0, len(rows))
	for _, r := range rows {
		src, err := s.sourceValue(r)
		if err != nil {
			return nil, err
		}
		out = append(out, src)
	}
	return out, nil
}

// SetSourceSigning moves a source from one signing state to another, and
// refuses when the stored state is no longer from.
func (s *EDA) SetSourceSigning(ctx context.Context, path string, from, to source.Signing) error {
	n, err := s.q.SetSourceSigning(ctx, postgresdb.SetSourceSigningParams{
		Path: path, FromSigning: string(from), ToSigning: string(to),
	})
	if err != nil {
		return fmt.Errorf("edastore: set source signing: %w", err)
	}
	if n == 1 {
		return nil
	}
	exists, err := s.q.SourceExists(ctx, path)
	if err != nil {
		return fmt.Errorf("edastore: set source signing: %w", err)
	}
	if !exists {
		return storecontract.ErrSourceNotFound
	}
	return storecontract.ErrSourceSigningStale
}

func (s *EDA) SetSourceSecret(ctx context.Context, path, secret string) error {
	sealed, err := s.sealSecret(secret)
	if err != nil {
		return err
	}
	n, err := s.q.SetSourceSecret(ctx, postgresdb.SetSourceSecretParams{Path: path, Secret: sealed})
	if err != nil {
		return fmt.Errorf("edastore: set source secret: %w", err)
	}
	if n == 0 {
		return storecontract.ErrSourceNotFound
	}
	return nil
}

// sealSecret encrypts a non-empty secret when a cipher is configured.
func (s *EDA) sealSecret(secret string) (string, error) {
	if s.cipher == nil || secret == "" {
		return secret, nil
	}
	sealed, err := s.cipher.Encrypt(secret)
	if err != nil {
		return "", fmt.Errorf("edastore: encrypt source secret: %w", err)
	}
	return sealed, nil
}

func (s *EDA) sourceValue(r postgresdb.Source) (source.Source, error) {
	secret := r.Secret
	if s.cipher != nil && secret != "" {
		plain, err := s.cipher.Decrypt(secret)
		if err != nil {
			return source.Source{}, fmt.Errorf("edastore: decrypt source secret: %w", err)
		}
		secret = plain
	}
	return source.Source{
		Path: r.Path, Signing: source.Signing(r.Signing), Secret: secret,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}, nil
}
