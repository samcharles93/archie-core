package archied

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgstore"
	"github.com/samcharles93/archie-core/internal/webui"
)

// TestPublishConfigSnapshotRendersTheDaemonsConfiguration: the daemon is the
// configuration owner, so it is the process that renders the dashboard's
// projection and publishes it for the UI process to read. It builds that view
// from its own state rather than from a webui.Server it never serves
// (archie-core-ml30).
func TestPublishConfigSnapshotRendersTheDaemonsConfiguration(t *testing.T) {
	st := pgstore.Open(t)
	t.Cleanup(func() { _ = st.Close() })

	cfg := config.Config{
		BotUser: "archie-bot",
		Repos:   []config.Repo{{Owner: "acme", Name: "widget", Base: "main"}},
		Chat: config.ChatConfig{
			Operator:      "Sam",
			ShowToolCalls: true,
			WebhookAddr:   "127.0.0.1:9099",
		},
	}
	b := &boot{
		log:        slog.New(slog.DiscardHandler),
		stateStore: st,
		cfgHolder:  config.NewHolder(cfg),
		lastReload: func() config.ReloadStatus {
			return config.ReloadStatus{LastError: "poll_interval must be positive"}
		},
	}
	b.currentProvenance.Store(&configuration.Provenance{Origins: []configuration.Origin{
		{Path: "/etc/archie/config.toml", Role: configuration.RoleMain, Layer: configuration.LayerBase},
	}})

	b.publishConfigSnapshot(t.Context())

	snapshot, found, err := st.ConfigSnapshot(context.Background())
	if err != nil || !found {
		t.Fatalf("ConfigSnapshot = (found %v, %v), want the published document", found, err)
	}
	if snapshot.Schema != webui.ConfigViewSchema {
		t.Fatalf("Schema = %q, want %q", snapshot.Schema, webui.ConfigViewSchema)
	}

	var view webui.ConfigView
	if err := json.Unmarshal(snapshot.Document, &view); err != nil {
		t.Fatalf("decode published document: %v", err)
	}
	if view.Identity.BotUser != "archie-bot" || len(view.Repositories) != 1 {
		t.Errorf("view = %+v, want the daemon's identity and repositories", view)
	}
	// The chat page and the setup checklist are rendered by a process that
	// holds no configuration, so the projection has to carry what they read.
	if !view.Chat.ShowToolCalls {
		t.Error("Chat.ShowToolCalls = false; the chat page would never expand tool calls")
	}
	if view.Chat.Operator != "Sam" {
		t.Errorf("Chat.Operator = %q, want Sam", view.Chat.Operator)
	}
	if !view.Chat.ChannelConfigured {
		t.Error("Chat.ChannelConfigured = false despite a configured webhook address")
	}
	if len(view.Provenance) != 1 || view.Provenance[0].Path != "/etc/archie/config.toml" {
		t.Errorf("Provenance = %+v, want the daemon's file chain", view.Provenance)
	}
	if view.Reload == nil || view.Reload.LastError != "poll_interval must be positive" {
		t.Errorf("Reload = %+v, want the last reload outcome", view.Reload)
	}
}

func TestPublishedSnapshotCarriesPerIdentityForges(t *testing.T) {
	st := pgstore.Open(t)
	t.Cleanup(func() { _ = st.Close() })

	b := &boot{
		log:        slog.New(slog.DiscardHandler),
		stateStore: st,
		cfgHolder: config.NewHolder(config.Config{
			BotUser: "archie-bot",
			Forge:   config.Forge{Type: "github", Host: "https://github.example"},
			Repos:   []config.Repo{{Owner: "acme", Name: "widget", Base: "main"}},
			Identities: []config.IdentityConfig{
				{
					Name: "gitea-bot", BotUser: "archie-gitea",
					Forge: config.Forge{Type: "gitea", Host: "https://gitea.example"},
					Repos: []config.Repo{{Owner: "beta", Name: "svc", Base: "main"}},
				},
			},
		}),
	}
	b.publishConfigSnapshot(t.Context())

	snapshot, found, err := st.ConfigSnapshot(context.Background())
	if err != nil || !found {
		t.Fatalf("ConfigSnapshot = (found %v, %v), want the published document", found, err)
	}
	var view webui.ConfigView
	if err := json.Unmarshal(snapshot.Document, &view); err != nil {
		t.Fatalf("decode published document: %v", err)
	}
	if !view.MultiIdentity {
		t.Error("MultiIdentity = false with a configured identity")
	}
	if len(view.Identities) != 1 {
		t.Fatalf("Identities = %+v, want the configured identity", view.Identities)
	}
	got := view.Identities[0]
	if got.Name != "gitea-bot" || got.ForgeType != "gitea" || got.ForgeHost != "https://gitea.example" {
		t.Errorf("Identities[0] = %+v, want the gitea-bot forge coordinates", got)
	}
	if len(got.Repos) != 1 || got.Repos[0].Owner != "beta" || got.Repos[0].Name != "svc" {
		t.Errorf("Identities[0].Repos = %+v, want the repositories it owns", got.Repos)
	}
}

// TestPublishConfigSnapshotWithoutConfiguration: called before the daemon has
// a configuration Holder, publishing is a no-op rather than a panic.
func TestPublishConfigSnapshotWithoutConfiguration(t *testing.T) {
	st := pgstore.Open(t)
	t.Cleanup(func() { _ = st.Close() })

	b := &boot{log: slog.New(slog.DiscardHandler), stateStore: st}
	b.publishConfigSnapshot(t.Context())

	if _, found, err := st.ConfigSnapshot(context.Background()); found || err != nil {
		t.Fatalf("ConfigSnapshot = (found %v, %v), want nothing published", found, err)
	}
}
