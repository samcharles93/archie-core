package archied

import (
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
)

// TestBuildTelegramRouterLeavesRestartUnwired pins that the Telegram router
// never wires Router.Restart, regardless of whether a Telegram adapter is in
// scope. Telegram's /restart is served by a dedicated exact-match handler
// (registerCommandHandlers → restartHandler), which is the only path that
// reaches RequestRestart; the router's own handleRestartAdapter is never
// reached on Telegram, so wiring Router.Restart here would be unreachable in
// production. It must stay nil and the router's /restart reply must keep
// reporting not-configured.
func TestBuildTelegramRouterLeavesRestartUnwired(t *testing.T) {
	tests := []struct {
		name string
		tg   *telegram.Gateway
	}{
		{name: "telegram adapter present", tg: telegram.New("test-token", nil, slog.Default())},
		{name: "no telegram adapter", tg: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			sessions := gateway.NewSessionStoreMemory()
			t.Cleanup(func() { _ = sessions.Close() })

			router := buildTelegramRouter(ctx, tt.tg, telegramSetup{
				Cfg:          config.NewHolder(config.Config{}),
				SessionStore: sessions,
				Log:          slog.Default(),
			}, sessions)

			if router.Restart != nil {
				t.Fatalf("router.Restart = %T, want nil: Telegram /restart is served by restartHandler, not this router", router.Restart)
			}

			reply, err := router.Route(ctx, gateway.Inbound{Message: messaging.Message{
				Role: messaging.RoleUser,
				Text: "/restart",
			}})
			if err != nil {
				t.Fatalf("Route(/restart) error = %v", err)
			}
			if reply != "Chat adapter restart is not configured." {
				t.Fatalf("Route(/restart) = %q, want %q", reply, "Chat adapter restart is not configured.")
			}
		})
	}
}
