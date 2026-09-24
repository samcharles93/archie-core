package archied

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/webui"
)

func TestDiskProbePath_PrefersStateDir(t *testing.T) {
	cfg := config.Config{StateDir: "/var/lib/archie", DBPath: "/data/archie/tasks.db", WorkDir: "/srv/archie"}
	if got := diskProbePath(cfg); got != "/var/lib/archie" {
		t.Fatalf("diskProbePath = %q, want /var/lib/archie", got)
	}
}

func TestDiskProbePath_FallsBackToWorkDir(t *testing.T) {
	cfg := config.Config{DBPath: "/data/archie/tasks.db", WorkDir: "/srv/archie"}
	if got := diskProbePath(cfg); got != "/srv/archie" {
		t.Fatalf("diskProbePath = %q, want /srv/archie", got)
	}
}

func TestDiskProbePath_FallsBackToCwd(t *testing.T) {
	if got := diskProbePath(config.Config{}); got != "." {
		t.Fatalf("diskProbePath = %q, want .", got)
	}
}

// TestPingChat_ReportsUnwiredChat pins that a daemon with no Gateway contract
// degrades its gateway probe rather than passing it: chat is served entirely
// by the Gateway now, so an unwired contract means no chat at all.
func TestPingChat_ReportsUnwiredChat(t *testing.T) {
	if err := pingChat(context.Background(), nil); err == nil {
		t.Fatal("pingChat(nil chat) = nil, want an error")
	}
	if err := pingChat(context.Background(), &webui.ChatService{}); err == nil {
		t.Fatal("pingChat(unwired contract) = nil, want an error")
	}
}

// TestSetupReadinessProbes_WiresEverySubsystem proves the composition root
// registers all five readiness probes and that the registry surfaces them in
// the detailed report. It does not assert their status -- real state depends
// on the store, config, disk, model and gateway in the running daemon -- only
// that every subsystem the epic names is actually wired.
func TestSetupReadinessProbes_WiresEverySubsystem(t *testing.T) {
	st := pgstore.Open(t)
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Config{
		BotUser:      "archie-bot",
		DBPath:       filepath.Join(t.TempDir(), "tasks.db"),
		PollInterval: config.Duration(60_000_000_000), // 60s, satisfies validate
		Dispatch:     config.Dispatch{Trigger: "assignee"},
		Forge:        config.Forge{Type: "none"},
		Containers: config.ContainerConfig{
			Image: "ghcr.io/samcharles93/archie-agent:latest",
		},
		Providers: map[string]config.Provider{
			"openai": {Class: "openai"},
		},
	}

	b := &boot{
		st:        st,
		cfgHolder: config.NewHolder(cfg),
		chat: &webui.ChatService{
			Contract: &gateway.LocalChatAdapter{
				Router:   &gateway.Router{},
				Sessions: gateway.NewSessionStoreMemory(),
				Models:   newChatModelManager(map[string]string{"chat": "openai/gpt-4o"}, nil),
			},
		},
		chatModels: newChatModelManager(map[string]string{"chat": "openai/gpt-4o"}, nil),
		cfg:        cfg,
	}

	b.setupReadinessProbes()
	if b.healthRegistry == nil {
		t.Fatal("b.healthRegistry is nil after setupReadinessProbes")
	}

	report := b.healthRegistry.Run(context.Background())
	if len(report.Components) != 5 {
		t.Fatalf("components = %d, want 5: %+v", len(report.Components), report.Components)
	}
	want := []string{"state_db", "config", "disk", "model", "gateway"}
	for i, name := range want {
		if report.Components[i].Name != name {
			t.Fatalf("component[%d] = %q, want %q", i, report.Components[i].Name, name)
		}
	}
}
