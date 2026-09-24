package archied

import (
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
	"github.com/samcharles93/archie-core/internal/store"
)

func TestRemoteChatCompositionPreservesChannelTurnLedger(t *testing.T) {
	cfg := config.Config{
		DBPath:      filepath.Join(t.TempDir(), "tasks.db"),
		DatabaseURL: pgtest.URL(t),
		Services:    config.Services{config.ServiceNameGateway: {Target: "127.0.0.1:1"}},
	}
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "state-store.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := &boot{cfg: cfg, log: slog.Default(), cfgHolder: config.NewHolder(cfg), stateStore: st, personas: gateway.NewPersonaRegistry(gateway.DefaultPersonas())}
	t.Cleanup(b.cleanup)
	if err := b.openStores(t.Context()); err != nil {
		t.Fatal(err)
	}
	if b.chatSessionStore == nil {
		t.Fatal("startup did not open the channel conversation store")
	}
	sessions := b.chatSessionStore
	if err := b.setupLLMAndChat(t.Context()); err != nil {
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
