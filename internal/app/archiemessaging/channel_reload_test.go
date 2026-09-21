package archiemessaging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/domain/messaging"
)

// TestReloadChannelRefusesWhatCannotReload: the dashboard offers the action on the
// strength of the capability its descriptor declares, so a channel that cannot
// reload must be refused with a reason. Accepting and doing nothing is the failure
// an operator cannot see through.
func TestReloadChannelRefusesWhatCannotReload(t *testing.T) {
	srv, err := compose(t.Context(), deps{
		Config: ResolvedConfig{
			WebhookAddr: "127.0.0.1:0",
			Webhook:     config.WebhookRoute{Path: "/smoke"},
		},
		Log:  slog.New(slog.DiscardHandler),
		Chat: &recordingChatContract{routes: make(chan messaging.Inbound, 1), reply: "ok"},
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	err = srv.ReloadChannel(t.Context(), "webhook")
	if err == nil {
		t.Fatal("ReloadChannel(webhook) = nil, want a refusal: webhook declares no reload support")
	}
	if !strings.Contains(err.Error(), "does not support reload") {
		t.Errorf("error = %v, want it to say the channel does not support reload", err)
	}

	if err := srv.ReloadChannel(t.Context(), "nope"); err == nil || !strings.Contains(err.Error(), "no channel") {
		t.Errorf("ReloadChannel(unknown) = %v, want it to name the missing channel", err)
	}
}

// TestReloadChannelReachesTheChannelsOwnSeam: the service routes a reload to the
// channel's own implementation rather than reimplementing it, so the operator's
// action and the front-end's own /restart path do the same thing.
func TestReloadChannelReachesTheChannelsOwnSeam(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	write := func(userIDs string) {
		content := "bot_user = \"testbot\"\n\n[chat.telegram]\ntoken_env = \"TEST_TG_TOKEN\"\nallowed_user_ids = " + userIDs + "\n"
		if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("[1]")
	t.Setenv("TEST_TG_TOKEN", "first-token")

	resolved, err := Resolve(Options{Config: cfgPath}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	srv, err := compose(t.Context(), deps{Config: resolved, Log: slog.New(slog.DiscardHandler), Chat: &recordingChatContract{}})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	gateway := telegramChannel(t, srv)

	write("[1, 2]")
	t.Setenv("TEST_TG_TOKEN", "second-token")
	if err := srv.ReloadChannel(t.Context(), "telegram"); err != nil {
		t.Fatalf("ReloadChannel(telegram): %v", err)
	}
	if len(gateway.AllowedUserIDs) != 2 || gateway.Token != "second-token" {
		t.Fatalf("after reload: allowlist %v, token %q; want the re-read config", gateway.AllowedUserIDs, gateway.Token)
	}
}

// TestReloadCapabilityAndActionShareOneSource pins the property that keeps the
// dashboard honest: whatever a descriptor declares reloadable must be reloadable,
// and vice versa, because both come from reloadable().
func TestReloadCapabilityAndActionShareOneSource(t *testing.T) {
	srv, err := compose(t.Context(), deps{
		Config: ResolvedConfig{
			TelegramToken: "token-123",
			Telegram:      config.TelegramConfig{TokenEnv: "TEST_TG_TOKEN", AllowedUserIDs: []int64{1}},
			WebhookAddr:   "127.0.0.1:0",
			Webhook:       config.WebhookRoute{Path: "/smoke"},
		},
		Log:  slog.New(slog.DiscardHandler),
		Chat: &recordingChatContract{routes: make(chan messaging.Inbound, 1), reply: "ok"},
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	for _, report := range srv.ChannelStatus() {
		err := srv.ReloadChannel(t.Context(), report.ID)
		// A declared reload can still fail on the configuration it re-reads --
		// telegram refuses a config that lost its token, and the existing test for
		// that pins it. What the capability claims is that the ACTION exists and is
		// routed, so the property tested here is that the refusal is specifically
		// "does not support reload".
		unsupported := err != nil && strings.Contains(err.Error(), "does not support reload")
		if report.ReloadSupported == unsupported {
			t.Errorf("%s: ReloadSupported=%v but reload returned %v; the declaration and the action have drifted",
				report.ID, report.ReloadSupported, err)
		}
	}
}
