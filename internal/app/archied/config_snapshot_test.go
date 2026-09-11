package archied

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration/overlay"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/webui"
)

// TestPublishConfigSnapshotRendersTheDaemonsConfiguration: the daemon is the
// configuration owner, so it is the process that renders the dashboard's
// projection and publishes it for the UI process to read. It builds that view
// from its own state rather than from a webui.Server it never serves
// (archie-core-ml30).
func TestPublishConfigSnapshotRendersTheDaemonsConfiguration(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
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
	// Editable describes the rendering process's write path, never the
	// published document.
	if view.Editable {
		t.Error("the published document claims to be editable")
	}
}

// TestPublishedSnapshotCarriesOverlayOverrides: the dashboard marks rows the
// runtime overlay shadows, so the projection has to name those keys. The
// daemon reads them from the overlay store it owns.
func TestPublishedSnapshotCarriesOverlayOverrides(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	overlayStore, err := overlay.Open(t.Context(), filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = overlayStore.Close() })
	if err := overlayStore.Set(t.Context(), "label", `"archie"`, "test"); err != nil {
		t.Fatal(err)
	}
	if err := overlayStore.Set(t.Context(), "budgets.max_steps", "40", "test"); err != nil {
		t.Fatal(err)
	}

	b := &boot{
		log:          slog.New(slog.DiscardHandler),
		stateStore:   st,
		cfgHolder:    config.NewHolder(config.Config{}),
		overlayStore: overlayStore,
	}
	b.publishConfigSnapshot(t.Context())

	snapshot, found, err := st.ConfigSnapshot(context.Background())
	if err != nil || !found {
		t.Fatalf("ConfigSnapshot = (found %v, %v), want the published document", found, err)
	}
	var view webui.ConfigView
	if err := json.Unmarshal(snapshot.Document, &view); err != nil {
		t.Fatal(err)
	}
	want := []string{"budgets.max_steps", "label"}
	if len(view.Overridden) != len(want) {
		t.Fatalf("Overridden = %+v, want %+v", view.Overridden, want)
	}
	for i, key := range want {
		if view.Overridden[i] != key {
			t.Errorf("Overridden[%d] = %q, want %q (sorted)", i, view.Overridden[i], key)
		}
	}
}

// TestPublishConfigSnapshotWithoutConfiguration: called before the daemon has
// a configuration Holder, publishing is a no-op rather than a panic.
func TestPublishConfigSnapshotWithoutConfiguration(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	b := &boot{log: slog.New(slog.DiscardHandler), stateStore: st}
	b.publishConfigSnapshot(t.Context())

	if _, found, err := st.ConfigSnapshot(context.Background()); found || err != nil {
		t.Fatalf("ConfigSnapshot = (found %v, %v), want nothing published", found, err)
	}
}
