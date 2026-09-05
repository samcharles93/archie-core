package archied

import (
	"context"
	"path/filepath"
	"testing"

	channelruntime "github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

func TestDiskProbePath_PrefersDBPath(t *testing.T) {
	cfg := config.Config{DBPath: "/data/archie/tasks.db", WorkDir: "/srv/archie"}
	if got := diskProbePath(cfg); got != "/data/archie" {
		t.Fatalf("diskProbePath = %q, want /data/archie", got)
	}
}

func TestDiskProbePath_FallsBackToWorkDir(t *testing.T) {
	cfg := config.Config{WorkDir: "/srv/archie"}
	if got := diskProbePath(cfg); got != "/srv/archie" {
		t.Fatalf("diskProbePath = %q, want /srv/archie", got)
	}
}

func TestDiskProbePath_FallsBackToCwd(t *testing.T) {
	if got := diskProbePath(config.Config{}); got != "." {
		t.Fatalf("diskProbePath = %q, want .", got)
	}
}

func TestChannelStates_ProjectsSnapshot(t *testing.T) {
	m := channelruntime.NewManager([]channelruntime.Descriptor{
		{ID: "telegram", Name: "Telegram", Configured: true},
		{ID: "email", Name: "Email", Configured: false},
	})
	m.MarkRunning("telegram")
	states := channelStates(m)
	if len(states) != 2 {
		t.Fatalf("states = %d, want 2", len(states))
	}
	if states[0].ID != "telegram" || !states[0].Configured || states[0].State != "running" {
		t.Fatalf("states[0] = %+v, want telegram running", states[0])
	}
	if states[1].ID != "email" || states[1].Configured || states[1].State != "stopped" {
		t.Fatalf("states[1] = %+v, want email stopped", states[1])
	}
}

func TestChannelStates_NilManager(t *testing.T) {
	if got := channelStates(nil); got != nil {
		t.Fatalf("channelStates(nil) = %#v, want nil", got)
	}
}

func TestSessionCount_ToleratesUnwiredChat(t *testing.T) {
	if got := sessionCount(context.Background(), nil); got != 0 {
		t.Fatalf("sessionCount(nil chat) = %d, want 0", got)
	}
	if got := sessionCount(context.Background(), &webui.ChatService{}); got != 0 {
		t.Fatalf("sessionCount(unwired Sessions) = %d, want 0", got)
	}
}

// TestSetupReadinessProbes_WiresEverySubsystem proves the composition root
// registers all five readiness probes and that the registry surfaces them in
// the detailed report. It does not assert their status -- real state depends
// on the store, config, disk, model and gateway in the running daemon -- only
// that every subsystem the epic names is actually wired.
func TestSetupReadinessProbes_WiresEverySubsystem(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
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
		st: st,
		web: &webui.Server{
			Cfg:      config.NewHolder(cfg),
			Channels: channelruntime.NewManager([]channelruntime.Descriptor{{ID: "telegram", Name: "Telegram", Configured: true}}),
			Chat: &webui.ChatService{
				Sessions: gateway.NewSessionStoreMemory(),
				Models:   newChatModelManager(map[string]string{"chat": "openai/gpt-4o"}, nil),
			},
		},
		chatModels: newChatModelManager(map[string]string{"chat": "openai/gpt-4o"}, nil),
		cfg:        cfg,
	}

	b.setupReadinessProbes()
	if b.web.Health == nil {
		t.Fatal("b.web.Health is nil after setupReadinessProbes")
	}

	report := b.web.Health.Run(context.Background())
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
