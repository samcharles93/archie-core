package archied

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/infrastructure/modelcatalog"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/webui"
)

// catalogServer answers the model catalog read with a models.dev-shaped
// document. It is the outside dependency the refresh loop actually meets:
// modelcatalog.Load fetches this and caches what it read.
func catalogServer(t *testing.T, document func() map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(document())
	}))
	t.Cleanup(srv.Close)
	return srv
}

// catalogProvider is one usable provider entry: an environment variable that
// resolves (so modelcatalog keeps it) and the tool-callable models it offers.
func catalogProvider(id, env string, models ...string) map[string]any {
	entries := make(map[string]any, len(models))
	for _, model := range models {
		entries[model] = map[string]any{
			"id": model, "name": strings.ToUpper(model), "tool_call": true,
			"limit": map[string]any{"context": 128000, "output": 16000},
		}
	}
	return map[string]any{
		"id": id, "name": strings.ToUpper(id), "npm": "@ai-sdk/openai-compatible",
		"api": "https://" + id + ".test/v1", "env": []string{env}, "models": entries,
	}
}

// newCatalogBoot builds the smallest boot the refresh path runs against: the
// control plane reads behind runtimeConfig, the secrets registry the catalog
// resolves keys through, the chat runtime a refresh must reach, and a catalog
// endpoint a test controls.
func newCatalogBoot(t *testing.T, url string) *boot {
	t.Helper()
	b := newReloadBoot(t, &controlPlaneStub{values: databaseOwnedResources()})
	b.secrets = secret.NewRegistry()
	b.chatModels = newChatModelManager(fileConfig().Models)
	b.setLLM(agentexec.NewRuntime(executionProviders(fileConfig())))
	b.catalogURL = url
	b.catalogCachePath = filepath.Join(t.TempDir(), "models.json")
	if err := b.loadRuntimeConfig(t.Context()); err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	return b
}

func awaitCatalog(t *testing.T, b *boot, ref string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Contains(b.chatModels.Models(), ref) {
		if time.Now().After(deadline) {
			t.Fatalf("chat models = %v, want %s to appear within the refresh interval", b.chatModels.Models(), ref)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestRefreshModelCatalogRepublishesEveryProjectionSite: a catalog read after
// boot must reach everything derived from it -- the running config a workflow
// stage resolves model limits from, the dashboard's published catalog view,
// and the chat runtime's model list and details. A value that reaches the
// boot struct alone parses and does nothing.
func TestRefreshModelCatalogRepublishesEveryProjectionSite(t *testing.T) {
	srv := catalogServer(t, func() map[string]any {
		return map[string]any{"acme": catalogProvider("acme", "ACME_API_KEY", "acme-tool")}
	})
	t.Setenv("ACME_API_KEY", "resolved-by-the-secret-registry")
	b := newCatalogBoot(t, srv.URL)

	if snapshot, _ := b.catalogState(); len(snapshot.Providers) != 0 {
		t.Fatalf("boot catalog = %+v, want the empty snapshot this test starts from", snapshot.Providers)
	}

	if err := b.refreshModelCatalog(t.Context()); err != nil {
		t.Fatalf("refreshModelCatalog: %v", err)
	}

	if got := b.cfgHolder.Get().ModelLimits["acme/acme-tool"]; got.ContextWindow != 128000 || got.MaxOutputTokens != 16000 {
		t.Errorf("running model limits = %+v, want the refreshed catalog's capacity", got)
	}
	if _, models := b.catalogState(); !slices.Contains(models, "acme/acme-tool") {
		t.Errorf("catalog models = %v, want the refreshed reference", models)
	}

	view := webui.BuildConfigView(b.configViewInput(t.Context()))
	if len(view.Catalog) != 1 || view.Catalog[0].ID != "acme" {
		t.Fatalf("published catalog = %+v, want the refreshed provider", view.Catalog)
	}
	if !slices.Contains(view.Catalog[0].Models, "acme-tool") {
		t.Errorf("published catalog models = %v, want the refreshed model", view.Catalog[0].Models)
	}

	if !slices.Contains(b.chatModels.Models(), "acme/acme-tool") {
		t.Errorf("chat models = %v, want the refreshed reference offered", b.chatModels.Models())
	}
	if _, ok := b.chatModels.ModelDetails("acme/acme-tool"); !ok {
		t.Error("chat model details missing the refreshed model")
	}
	if got := b.chatModels.ProviderDisplayName("acme"); got != "ACME" {
		t.Errorf("provider display name = %q, want the refreshed catalog's name", got)
	}
}

// TestModelCatalogRefreshLoopPicksUpANewModelWhileTheDaemonRuns is the
// without-a-restart proof: the process is already running on the catalog it
// booted with, and a model that appears upstream afterwards is offered by the
// next tick.
func TestModelCatalogRefreshLoopPicksUpANewModelWhileTheDaemonRuns(t *testing.T) {
	var calls atomic.Int64
	srv := catalogServer(t, func() map[string]any {
		if calls.Add(1) == 1 {
			return map[string]any{"acme": catalogProvider("acme", "ACME_API_KEY", "acme-one")}
		}
		return map[string]any{"acme": catalogProvider("acme", "ACME_API_KEY", "acme-one", "acme-two")}
	})
	t.Setenv("ACME_API_KEY", "resolved-by-the-secret-registry")
	b := newCatalogBoot(t, srv.URL)
	if err := b.refreshModelCatalog(t.Context()); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if slices.Contains(b.chatModels.Models(), "acme/acme-two") {
		t.Fatal("the second model was offered before upstream published it")
	}

	b.catalogRefreshInterval = 5 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b.startModelCatalogRefresh(ctx)

	awaitCatalog(t, b, "acme/acme-two")
	if got := b.cfgHolder.Get().ModelLimits["acme/acme-two"]; got.ContextWindow != 128000 {
		t.Errorf("running model limits for the new model = %+v, want the refreshed capacity", got)
	}
}

// TestRefreshModelCatalogKeepsTheLoadedCatalogWhenTheReadFails: the catalog
// service having a bad minute must not blank the model list the process is
// already running on. The failed read is reported; the snapshot stays.
func TestRefreshModelCatalogKeepsTheLoadedCatalogWhenTheReadFails(t *testing.T) {
	srv := catalogServer(t, func() map[string]any {
		return map[string]any{"acme": catalogProvider("acme", "ACME_API_KEY", "acme-one")}
	})
	t.Setenv("ACME_API_KEY", "resolved-by-the-secret-registry")
	b := newCatalogBoot(t, srv.URL)
	if err := b.refreshModelCatalog(t.Context()); err != nil {
		t.Fatalf("refreshModelCatalog: %v", err)
	}

	// The endpoint stops answering and the cache is gone, so the read has no
	// fallback left.
	srv.Close()
	if err := os.Remove(b.catalogCachePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("remove cache: %v", err)
	}
	if err := b.refreshModelCatalog(t.Context()); err == nil {
		t.Fatal("refreshModelCatalog error = nil, want the failed read reported")
	}

	if _, models := b.catalogState(); !slices.Contains(models, "acme/acme-one") {
		t.Errorf("catalog models = %v, want the loaded catalog kept", models)
	}
	if got := b.cfgHolder.Get().ModelLimits["acme/acme-one"]; got.ContextWindow != 128000 {
		t.Errorf("running model limits = %+v, want the last-known-good catalog", got)
	}
}

// TestBootCatalogReachesTheRunningConfig is the projection-site check for the
// boot half: the catalog boot read must be in the config the process runs on,
// not only in the boot struct the chat runtime was built from. Otherwise the
// first live update is the first snapshot to carry a catalog model limit.
func TestBootCatalogReachesTheRunningConfig(t *testing.T) {
	srv := catalogServer(t, func() map[string]any {
		return map[string]any{"acme": catalogProvider("acme", "ACME_API_KEY", "acme-one")}
	})
	t.Setenv("ACME_API_KEY", "resolved-by-the-secret-registry")
	b := newCatalogBoot(t, srv.URL)

	b.loadCatalog(t.Context(), filepath.Join(t.TempDir(), "config.toml"))

	if got := b.cfgHolder.Get().ModelLimits["acme/acme-one"]; got.ContextWindow != 128000 {
		t.Errorf("running model limits = %+v, want the catalog boot loaded", got)
	}
	if _, models := b.catalogState(); !slices.Contains(models, "acme/acme-one") {
		t.Errorf("catalog models = %v, want the boot catalog", models)
	}
}

// TestChatModelManagerReplacesItsCatalogRatherThanMerging: a refresh must
// drop what the previous snapshot contributed. A manager that only merged
// would keep offering a model the catalog no longer publishes, and would
// describe a provider that no longer exists.
func TestChatModelManagerReplacesItsCatalogRatherThanMerging(t *testing.T) {
	manager := newChatModelManager(map[string]string{"chat": "acme/one"})
	manager.SetModelCatalog(
		modelcatalog.Snapshot{Providers: []modelcatalog.Provider{
			{ID: "acme", Name: "Acme", Models: []modelcatalog.Model{{ID: "one"}, {ID: "two"}}},
		}},
		[]string{"acme/one", "acme/two"},
	)
	if !slices.Contains(manager.Models(), "acme/two") {
		t.Fatalf("chat models = %v, want the catalog's models offered", manager.Models())
	}
	if got := manager.ProviderDisplayName("acme"); got != "Acme" {
		t.Fatalf("provider display name = %q, want the catalog's name", got)
	}

	manager.SetModelCatalog(
		modelcatalog.Snapshot{Providers: []modelcatalog.Provider{
			{ID: "acme", Name: "Acme", Models: []modelcatalog.Model{{ID: "one"}}},
		}},
		[]string{"acme/one"},
	)

	if slices.Contains(manager.Models(), "acme/two") {
		t.Errorf("chat models = %v, want the withdrawn model dropped", manager.Models())
	}
	if _, ok := manager.ModelDetails("acme/two"); ok {
		t.Error("chat model details still carry the withdrawn model")
	}
	if got := manager.ProviderDisplayName("gone"); got != "" {
		t.Errorf("provider display name for an absent provider = %q, want empty", got)
	}
	if !slices.Contains(manager.Models(), "acme/one") {
		t.Errorf("chat models = %v, want the surviving model kept", manager.Models())
	}
}
