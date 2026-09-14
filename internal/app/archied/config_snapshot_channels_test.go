package archied

import (
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// TestPublishedProjectionAgreesWithTheChannelManagerOnConfiguredChannels: the
// daemon answers "is a chat channel configured" twice -- once through the
// channel status manager it exposes on /api/channels, and once through the
// ChatView.ChannelConfigured boolean it publishes for the dashboard's setup
// checklist. Those two answers are read by different pages of the same
// extracted process, so a deployment that the manager calls configured must
// not be told by the checklist to connect a channel it already has. Both sides
// now derive from one definition (config.ChatConfig.FrontEnds); this test is
// what keeps them from being re-decided separately.
func TestPublishedProjectionAgreesWithTheChannelManagerOnConfiguredChannels(t *testing.T) {
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
			st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })

			b := &boot{
				cfg:        tc.cfg,
				log:        slog.New(slog.DiscardHandler),
				stateStore: st,
				cfgHolder:  config.NewHolder(tc.cfg),
			}
			b.setupObservability(t.Context())

			// What the dashboard's /api/channels page reads.
			managerSaysConfigured := false
			for _, descriptor := range b.channelManager.Snapshot() {
				if descriptor.Configured {
					managerSaysConfigured = true
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

			if published.Chat.ChannelConfigured != managerSaysConfigured {
				t.Errorf("published Chat.ChannelConfigured = %v, but the channel manager reports a configured front-end = %v (%+v)",
					published.Chat.ChannelConfigured, managerSaysConfigured, published.Chat)
			}
		})
	}
}
