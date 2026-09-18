package archied

import (
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/channels/telegram"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
	"github.com/samcharles93/archie-core/internal/gateway"
)

// TestBuildTelegramRouterWiresRestartCallback drives the same Router
// composition path setupTelegramGateway uses and pins that the Telegram
// adapter's restart callback is wired into Router.Restart. The daemon used to
// capture tg.RequestRestart into a boot field and never read it, leaving
// Router.Restart nil so /restart always answered "not configured".
func TestBuildTelegramRouterWiresRestartCallback(t *testing.T) {
	ctx := t.Context()
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })

	tg := telegram.New("test-token", nil, slog.Default())
	router := buildTelegramRouter(ctx, tg, telegramSetup{
		Cfg:          config.NewHolder(config.Config{}),
		SessionStore: sessions,
		Log:          slog.Default(),
	}, sessions)

	if router.Restart == nil {
		t.Fatal("router.Restart = nil, want the telegram adapter restart callback wired into the router")
	}

	// Invoking the router's restart path must call the adapter's restart
	// callback exactly once: the first call queues the scoped reload and
	// returns nil; a second call is rejected because a restart is already
	// in progress.
	if err := router.Restart(ctx); err != nil {
		t.Fatalf("router.Restart = %v, want nil (callback called exactly once)", err)
	}
	if err := router.Restart(ctx); err == nil {
		t.Fatal("router.Restart second call = nil, want error: telegram gateway restart already in progress")
	}

	// The wired router must also surface the capability to the local chat
	// snapshot, which is what the web UI's restart affordance reads.
	adapter := &gateway.LocalChatAdapter{Router: router, Sessions: sessions}
	snapshot, err := adapter.Snapshot(ctx)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !snapshot.RestartAvailable {
		t.Fatal("snapshot.RestartAvailable = false, want true when the restart callback is wired")
	}
}

// TestBuildTelegramRouterRestartUnconfigured pins the unchanged behaviour
// when no Telegram adapter exists: there is no restart callback, so
// Router.Restart stays nil and /restart still reports "not configured".
func TestBuildTelegramRouterRestartUnconfigured(t *testing.T) {
	ctx := t.Context()
	sessions := gateway.NewSessionStoreMemory()
	t.Cleanup(func() { _ = sessions.Close() })

	router := buildTelegramRouter(ctx, nil, telegramSetup{
		Cfg:          config.NewHolder(config.Config{}),
		SessionStore: sessions,
		Log:          slog.Default(),
	}, sessions)

	if router.Restart != nil {
		t.Fatal("router.Restart = non-nil with no telegram adapter, want nil")
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
}
