package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/bindingcipher"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/postgresdb"
)

var _ storecontract.HarnessSecretStore = (*EDA)(nil)

// harnessSecretPayload is the JSON shape sealed into harness_secrets.secret_enc:
// the whole token set behind one envelope, never separate plaintext columns.
type harnessSecretPayload struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes,omitempty"`
}

// GetHarnessSecret returns the stored OAuth token set for an org/service
// credential binding, or storecontract.ErrHarnessSecretNotFound.
func (s *EDA) GetHarnessSecret(ctx context.Context, org, service string) (harnesssecret.Secret, error) {
	row, err := s.q.GetHarnessSecret(ctx, postgresdb.GetHarnessSecretParams{Org: org, Service: service})
	if errors.Is(err, pgx.ErrNoRows) {
		return harnesssecret.Secret{}, storecontract.ErrHarnessSecretNotFound
	}
	if err != nil {
		return harnesssecret.Secret{}, fmt.Errorf("postgres: get harness secret: %w", err)
	}
	payload, err := s.openHarnessSecret(row.SecretEnc)
	if err != nil {
		return harnesssecret.Secret{}, err
	}
	return harnesssecret.Secret{
		Org: row.Org, Service: row.Service,
		AccessToken: payload.AccessToken, RefreshToken: payload.RefreshToken,
		TokenType: payload.TokenType, ExpiresAt: payload.ExpiresAt,
		Scopes:    payload.Scopes,
		UpdatedAt: row.UpdatedAt,
	}, nil
}

// PutHarnessSecret upserts an org/service credential binding's OAuth token
// set. Called by the setup terminal's first capture and by the egress
// proxy's every refresh.
func (s *EDA) PutHarnessSecret(ctx context.Context, secret harnesssecret.Secret) error {
	if err := secret.Validate(); err != nil {
		return err
	}
	sealed, err := s.sealHarnessSecret(harnessSecretPayload{
		AccessToken: secret.AccessToken, RefreshToken: secret.RefreshToken,
		TokenType: secret.TokenType, ExpiresAt: secret.ExpiresAt,
		Scopes: secret.Scopes,
	})
	if err != nil {
		return err
	}
	if err := s.q.PutHarnessSecret(ctx, postgresdb.PutHarnessSecretParams{
		Org: secret.Org, Service: secret.Service, SecretEnc: sealed,
	}); err != nil {
		return fmt.Errorf("postgres: put harness secret: %w", err)
	}
	return nil
}

// sealHarnessSecret JSON-marshals the token set and encrypts it under
// bindingcipher.HarnessSecretDomain -- its own domain separator, distinct
// from BindingDomain's, so a row cannot be relocated between the two tables
// and still authenticate. OAuth tokens require a configured encryption key;
// the binding store's legacy plaintext fallback does not apply here.
func (s *EDA) sealHarnessSecret(payload harnessSecretPayload) (string, error) {
	if s.cipher == nil {
		return "", errors.New("postgres: harness secrets require an encryption key")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("postgres: marshal harness secret: %w", err)
	}
	sealed, err := s.cipher.EncryptDomain(bindingcipher.HarnessSecretDomain, string(raw))
	if err != nil {
		return "", fmt.Errorf("postgres: encrypt harness secret: %w", err)
	}
	return sealed, nil
}

// openHarnessSecret reverses sealHarnessSecret.
func (s *EDA) openHarnessSecret(stored string) (harnessSecretPayload, error) {
	if s.cipher == nil {
		return harnessSecretPayload{}, errors.New("postgres: harness secrets require an encryption key")
	}
	raw, err := s.cipher.DecryptDomain(bindingcipher.HarnessSecretDomain, stored)
	if err != nil {
		return harnessSecretPayload{}, fmt.Errorf("postgres: decrypt harness secret: %w", err)
	}
	var payload harnessSecretPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return harnessSecretPayload{}, fmt.Errorf("postgres: unmarshal harness secret: %w", err)
	}
	return payload, nil
}
