package archied

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/webui"
)

// TestPublishedProjectionAgreesWithFrontEndsOnConfiguredChannels: "is a chat
// channel configured" is answered twice -- once by config.ChatConfig.FrontEnds,
// which the channel status surface on /api/channels is built from, and once by
// the ChatView.ChannelConfigured boolean this process publishes for the
// dashboard's setup checklist. A deployment that FrontEnds calls configured
// must not be told by the checklist to connect a channel it already has. Both
// sides derive from that one definition; this test is what keeps them from
// being re-decided separately.
func TestPublishedProjectionAgreesWithFrontEndsOnConfiguredChannels(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
	}{
		{name: "nothing configured", cfg: config.Config{}},
		{
			name: "telegram token",
			cfg: config.Config{Chat: config.ChatConfig{
				Telegram: config.TelegramConfig{TokenEnv: "TELEGRAM_TOKEN"},
			}},
		},
		{
			name: "telegram token through the secret engine",
			cfg: config.Config{Chat: config.ChatConfig{
				Telegram: config.TelegramConfig{Token: config.SecretRef{Engine: "builtin", Key: "telegram"}},
			}},
		},
		{
			name: "webhook gateway",
			cfg:  config.Config{Chat: config.ChatConfig{WebhookAddr: "127.0.0.1:9099"}},
		},
		{
			name: "inbound mail gateway",
			cfg:  config.Config{Chat: config.ChatConfig{Email: config.EmailConfig{ListenAddr: "127.0.0.1:2525"}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := pgstore.Open(t)
			t.Cleanup(func() { _ = st.Close() })

			b := &boot{
				cfg:        tc.cfg,
				log:        slog.New(slog.DiscardHandler),
				stateStore: st,
				cfgHolder:  config.NewHolder(tc.cfg),
			}
			b.setupObservability(t.Context())

			// What the dashboard's /api/channels page is built from.
			frontEndsSayConfigured := false
			for _, frontEnd := range tc.cfg.Chat.FrontEnds() {
				if frontEnd.Configured {
					frontEndsSayConfigured = true
				}
			}

			// What the dashboard's setup checklist reads, through the
			// same round trip a reading process performs.
			document, err := json.Marshal(webui.BuildConfigView(b.configViewInput(t.Context())))
			if err != nil {
				t.Fatalf("render the published projection: %v", err)
			}
			var published webui.ConfigView
			if err := json.Unmarshal(document, &published); err != nil {
				t.Fatalf("decode the published projection: %v", err)
			}

			if published.Chat.ChannelConfigured != frontEndsSayConfigured {
				t.Errorf("published Chat.ChannelConfigured = %v, but ChatConfig.FrontEnds reports a configured front-end = %v (%+v)",
					published.Chat.ChannelConfigured, frontEndsSayConfigured, published.Chat)
			}
		})
	}
}
