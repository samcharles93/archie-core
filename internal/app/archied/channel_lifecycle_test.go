package archied

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	channelruntime "github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

func TestConfiguredNetworkGatewaysBecomeRunning(t *testing.T) {
	tests := []struct {
		name string
		id   string
		cfg  config.Config
	}{
		{
			name: "email",
			id:   "email",
			cfg: config.Config{Chat: config.ChatConfig{Email: config.EmailConfig{
				ListenAddr: "127.0.0.1:0",
			}}},
		},
		{
			name: "webhook",
			id:   "webhook",
			cfg: config.Config{Chat: config.ChatConfig{
				WebhookAddr: "127.0.0.1:0",
			}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "archie.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			manager := channelruntime.NewManager([]channelruntime.Descriptor{{
				ID: test.id, Name: test.name, Configured: true,
			}})
			b := &boot{
				cfg:            test.cfg,
				log:            slog.New(slog.DiscardHandler),
				st:             st,
				channelManager: manager,
				web:            &webui.Server{},
			}
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)

			if ok := b.setupGateways(ctx, "", ""); !ok {
				t.Fatal("setupGateways returned false")
			}
			if len(b.startGateways) != 1 {
				t.Fatalf("start gateways = %d, want 1", len(b.startGateways))
			}
			b.startGateways[0]()

			deadline := time.Now().Add(250 * time.Millisecond)
			for time.Now().Before(deadline) {
				if got := manager.Snapshot()[0].State; got == channelruntime.StateRunning {
					return
				}
				time.Sleep(time.Millisecond)
			}
			t.Fatalf("channel state = %q, want %q after successful startup",
				manager.Snapshot()[0].State, channelruntime.StateRunning)
		})
	}
}
