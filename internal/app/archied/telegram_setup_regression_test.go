package archied

import (
	"context"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/secret"
)

func TestBuildTelegramRouterWiresPersonas(t *testing.T) {
	personas := gateway.NewPersonaRegistry(gateway.DefaultPersonas())
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })

	router := buildTelegramRouter(context.Background(), nil, telegramSetup{
		Cfg:          config.NewHolder(config.Config{}),
		Personas:     personas,
		SessionStore: sessions,
		Log:          slog.Default(),
	}, sessions)

	if router.Personas != personas {
		t.Fatalf("router.Personas = %p, want the configured registry %p", router.Personas, personas)
	}
}

// TestSetupTelegramGatewayAcceptsTokenRefWithoutTokenEnv is the regression
// guard for channels-telegram-5: wiring Gateway.ValidateConfig into the
// startup path without first fixing it to accept a Token secret ref (not
// just TokenEnv) would have rejected every deployment configured via
// chat.telegram.token, even though resolveTelegramToken already treats
// Token as taking precedence over TokenEnv.
func TestSetupTelegramGatewayAcceptsTokenRefWithoutTokenEnv(t *testing.T) {
	registry := secret.NewRegistry()
	registry.Register(telegramTestSecretEngine{})
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })

	cfg := config.Config{Chat: config.ChatConfig{Telegram: config.TelegramConfig{
		Token: secret.SecretRef{Engine: "bws", Key: "telegram-token"},
		// TokenEnv deliberately left empty.
	}}}

	start, ok := setupTelegramGateway(context.Background(), telegramSetup{
		Cfg:          config.NewHolder(cfg),
		Secrets:      registry,
		SessionStore: sessions,
		Log:          slog.Default(),
	})
	if !ok {
		t.Fatal("setupTelegramGateway ok = false, want true: chat.telegram.token alone is a valid credential source")
	}
	if start == nil {
		t.Fatal("setupTelegramGateway start = nil, want a start function")
	}
}

// TestSetupTelegramGatewayRejectsMissingCredential is the companion
// negative case: with neither token nor token_env resolvable to a
// non-empty value, ValidateConfig's wiring must still refuse to start.
func TestSetupTelegramGatewayRejectsMissingCredential(t *testing.T) {
	registry := secret.NewRegistry()
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })

	cfg := config.Config{Chat: config.ChatConfig{Telegram: config.TelegramConfig{
		Token: secret.SecretRef{Engine: "bws", Key: "telegram-token"},
	}}}

	_, ok := setupTelegramGateway(context.Background(), telegramSetup{
		Cfg:          config.NewHolder(cfg),
		Secrets:      registry, // bws engine not registered: resolution fails, token stays empty
		SessionStore: sessions,
		Log:          slog.Default(),
	})
	if ok {
		t.Fatal("setupTelegramGateway ok = true, want false: token cannot be resolved")
	}
}
