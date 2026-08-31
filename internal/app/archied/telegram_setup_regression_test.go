package archied

import (
	"context"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
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
