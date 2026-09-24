package archiemessaging

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
)

type testSecretEngine struct{}

func (testSecretEngine) Name() string    { return "bws" }
func (testSecretEngine) Version() string { return "test" }
func (testSecretEngine) Resolve(key string) (string, error) {
	if key == "telegram-token" {
		return "token-from-bws", nil
	}
	return "", nil
}

func TestResolveTelegramTokenResolvesSecretRef(t *testing.T) {
	registry := secret.NewRegistry()
	registry.Register(testSecretEngine{})
	cfg := config.TelegramConfig{
		Token: secret.SecretRef{Engine: "bws", Key: "telegram-token"},
	}

	got, err := resolveTelegramToken(cfg, registry)
	if err != nil {
		t.Fatalf("resolveTelegramToken returned error: %v", err)
	}
	if got != "token-from-bws" {
		t.Fatalf("resolveTelegramToken = %q, want token-from-bws", got)
	}
}

// TestResolveTelegramTokenReportsUnresolvableRef is the companion negative
// case: an unregistered engine must surface as an error rather than an
// empty token that composes a channel nothing can authenticate.
func TestResolveTelegramTokenReportsUnresolvableRef(t *testing.T) {
	cfg := config.TelegramConfig{
		Token: secret.SecretRef{Engine: "nosuch", Key: "telegram-token"},
	}

	if _, err := resolveTelegramToken(cfg, secret.NewRegistry()); err == nil {
		t.Fatal("resolveTelegramToken error = nil, want an error: the engine is not registered")
	}
}
