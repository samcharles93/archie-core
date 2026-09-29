package postgres

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/samcharles93/archie-core/internal/domain/harnesssecret"
	"github.com/samcharles93/archie-core/internal/domain/storecontract"
	"github.com/samcharles93/archie-core/internal/infrastructure/bindingcipher"
)

func readRawHarnessSecret(t *testing.T, pool *pgxpool.Pool, org, service string) string {
	t.Helper()
	var stored string
	err := pool.QueryRow(t.Context(), "SELECT secret_enc FROM harness_secrets WHERE org = $1 AND service = $2", org, service).Scan(&stored)
	if err != nil {
		t.Fatalf("read raw harness secret: %v", err)
	}
	return stored
}

// A stored OAuth token set is a cipher envelope on disk and reads back as
// plaintext, and the column never holds the raw access or refresh token.
func TestHarnessSecretRoundTripEncrypts(t *testing.T) {
	cipher, err := bindingcipher.NewBindingCipher(edaTestKey, nil)
	if err != nil {
		t.Fatalf("NewBindingCipher() error = %v", err)
	}
	pool, s := edaWithCipher(t, cipher)
	in := harnesssecret.Secret{
		Org: "org-1", Service: "claude-code",
		AccessToken: "at-0123456789abcdef", RefreshToken: "rt-0123456789abcdef",
		TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour).UTC().Truncate(time.Second),
		Scopes: []string{"user:inference", "user:profile"},
	}
	if err := s.PutHarnessSecret(t.Context(), in); err != nil {
		t.Fatalf("PutHarnessSecret() error = %v", err)
	}
	raw := readRawHarnessSecret(t, pool, in.Org, in.Service)
	if raw == "" {
		t.Fatal("stored secret is empty")
	}
	for _, want := range []string{in.AccessToken, in.RefreshToken} {
		if strings.Contains(raw, want) {
			t.Fatalf("stored column contains a real token: %q", raw)
		}
	}
	got, err := s.GetHarnessSecret(t.Context(), in.Org, in.Service)
	if err != nil {
		t.Fatalf("GetHarnessSecret() error = %v", err)
	}
	if got.AccessToken != in.AccessToken || got.RefreshToken != in.RefreshToken ||
		got.TokenType != in.TokenType || !got.ExpiresAt.Equal(in.ExpiresAt) ||
		!slices.Equal(got.Scopes, in.Scopes) {
		t.Fatalf("GetHarnessSecret() = %+v, want %+v", got, in)
	}
}

// A refresh (a second Put for the same org/service) overwrites the row
// rather than accumulating one per token: exactly the upsert semantics the
// egress proxy's every refresh depends on.
func TestHarnessSecretPutOverwritesOnRefresh(t *testing.T) {
	cipher, err := bindingcipher.NewBindingCipher(edaTestKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, s := edaWithCipher(t, cipher)
	org, service := "org-1", "claude-code"
	first := harnesssecret.Secret{Org: org, Service: service, AccessToken: "at-first", RefreshToken: "rt-first"}
	if err := s.PutHarnessSecret(t.Context(), first); err != nil {
		t.Fatalf("PutHarnessSecret(first) error = %v", err)
	}
	second := harnesssecret.Secret{Org: org, Service: service, AccessToken: "at-second", RefreshToken: "rt-second"}
	if err := s.PutHarnessSecret(t.Context(), second); err != nil {
		t.Fatalf("PutHarnessSecret(second) error = %v", err)
	}
	got, err := s.GetHarnessSecret(t.Context(), org, service)
	if err != nil {
		t.Fatalf("GetHarnessSecret() error = %v", err)
	}
	if got.AccessToken != second.AccessToken {
		t.Fatalf("AccessToken = %q, want the refreshed value %q", got.AccessToken, second.AccessToken)
	}
}

func TestHarnessSecretNotFound(t *testing.T) {
	_, s := edaWithCipher(t, nil)
	_, err := s.GetHarnessSecret(t.Context(), "org-1", "no-such-service")
	if !errors.Is(err, storecontract.ErrHarnessSecretNotFound) {
		t.Fatalf("GetHarnessSecret() error = %v, want ErrHarnessSecretNotFound", err)
	}
}

func TestHarnessSecretPutRejectsIncomplete(t *testing.T) {
	_, s := edaWithCipher(t, nil)
	if err := s.PutHarnessSecret(t.Context(), harnesssecret.Secret{Org: "org-1"}); err == nil {
		t.Fatal("PutHarnessSecret() with no service or access token succeeded, want error")
	}
}

func TestHarnessSecretRequiresEncryption(t *testing.T) {
	pool, store := edaWithCipher(t, nil)
	err := store.PutHarnessSecret(t.Context(), harnesssecret.Secret{Org: "org", Service: "service", AccessToken: "access"})
	if err == nil {
		t.Fatal("OAuth token stored without an encryption key")
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM harness_secrets").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("stored %d rows without encryption", count)
	}
}
