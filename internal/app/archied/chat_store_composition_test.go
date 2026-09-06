package archied

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

func TestRemoteChatCompositionPreservesChannelTurnLedger(t *testing.T) {
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "tasks.db")}
	cfg.Services.Gateway.Target = "127.0.0.1:1"
	st, err := store.Open(t.Context(), cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := &boot{cfg: cfg, st: st, log: slog.Default(), web: &webui.Server{Cfg: config.NewHolder(cfg)}}
	t.Cleanup(b.cleanup)
	if err := b.openChatSessions(t.Context()); err != nil {
		t.Fatal(err)
	}
	sessions := b.chatSessionStore
	if err := b.setupLLMAndChat(); err != nil {
		t.Fatal(err)
	}
	if b.chatSessionStore != sessions {
		t.Fatal("remote web chat replaced the channel conversation store")
	}
	ledger, ok := b.chatSessionStore.(gateway.TurnLedger)
	if !ok {
		t.Fatal("channel conversation store does not implement TurnLedger")
	}
	if err := ledger.RecoverTurns(t.Context(), "test-owner"); err != nil {
		t.Fatal(err)
	}
}
