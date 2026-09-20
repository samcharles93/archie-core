package webui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/channels/status"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/store"
)

// fakeSecrets are recognisable strings that must never appear anywhere in
// handleConfig's response body. If any of these leak, the allowlist in
// api_config.go has a hole -- see handleConfig's doc comment for why it is
// built field by field rather than by marshalling config.Config directly.
const (
	fakeForgeToken  = "ghp_FAKESECRETTOKENVALUE123"
	fakeProviderKey = "sk-FAKEPROVIDERKEYVALUE456"
	fakeBWSKeyName  = "FAKE-BWS-SECRET-KEY-789"
	fakeNATSToken   = "nats-FAKESECRETTOKEN000"
)

func configWithFakeSecrets() *config.Holder {
	return config.NewHolder(config.Config{
		WorkDir:   "/work/archie",
		SkillsDir: "/work/archie/.agents/skills",
		DBPath:    "/work/archie/archie.db",
		BotUser:   "archie-bot",
		BotEmail:  "archie@example.com",
		Label:     "archie",
		Forge: config.Forge{
			Type:     "gitea",
			Host:     "gitea.example.com",
			TokenEnv: "GITEA_TOKEN",
			Token:    secret.SecretRef{Engine: "env", Key: fakeForgeToken},
		},
		Models: map[string]string{"builder": "openai/gpt-4"},
		Providers: map[string]config.Provider{
			"openai": {
				Class:     "openai",
				APIKeyEnv: "OPENAI_API_KEY",
				APIKey:    secret.SecretRef{Engine: "bws", Key: fakeBWSKeyName},
				BaseURL:   "https://api.openai.com/v1",
			},
			"custom": {
				Class:  "custom",
				APIKey: secret.SecretRef{Engine: "literal", Key: fakeProviderKey},
			},
		},
		Repos: []config.Repo{
			{Owner: "acme", Name: "widget", Base: "main", Gate: [][]string{{"task", "check"}}},
		},
		LegacyAgent: config.LegacyAgent{Mode: "subprocess", Command: "/usr/local/bin/archie-agent", Env: []string{"HOME"}},
		NATS:        config.NATSConfig{URL: "nats://127.0.0.1:4222", TokenEnv: "NATS_TOKEN"},
		Chat: config.ChatConfig{
			Telegram: config.TelegramConfig{TokenEnv: "TELEGRAM_TOKEN"},
		},
	})
}

// leakCandidates returns the set of fake secret strings a test should
// confirm are absent from a response body. NATS token is included even
// though handleConfig doesn't expose NATS today, guarding against a future
// regression that starts serializing it wholesale.
func leakCandidates() []string {
	return []string{fakeForgeToken, fakeProviderKey, fakeBWSKeyName, fakeNATSToken}
}

// localView installs the projection a configuration owner publishes, built by
// the same function the daemon calls. Tests that used to hand the server a
// live config.Holder now hand it that holder's projection, which is what every
// process renders from since the server stopped holding configuration
// (archie-core-ml30).
func localView(srv *Server, in ConfigViewInput) {
	srv.ConfigSource = func(context.Context) (ConfigView, bool, error) {
		return BuildConfigView(in), true, nil
	}
}

// localConfig is localView for the common case: a config snapshot and nothing
// else.
func localConfig(srv *Server, cfg *config.Holder) {
	if cfg == nil {
		return
	}
	localView(srv, ConfigViewInput{Config: cfg.Get()})
}

// TestBuildConfigViewPublishesPerIdentityForges: in a multi-identity
// deployment Identity and Repositories describe the default identity alone,
// which is not enough to attribute a task to the forge that owns it. The
// projection publishes each identity's forge coordinates and its repository
// list so a process rendering task rows can resolve per task
// (archie-core-pv6t) -- and, like every other field here, without a token.
func TestBuildConfigViewPublishesPerIdentityForges(t *testing.T) {
	cfg := config.Config{
		Forge: config.Forge{Type: "github", Host: "https://github.example"},
		Repos: []config.Repo{{Owner: "acme", Name: "widget"}},
		Identities: []config.IdentityConfig{
			{
				Name:  "gitea-bot",
				Forge: config.Forge{Type: "gitea", Host: "https://gitea.example", Token: secret.SecretRef{Engine: "env", Key: fakeForgeToken}},
				Repos: []config.Repo{{Owner: "acme", Name: "gadget"}, {Owner: "beta", Name: "svc"}},
			},
			{
				Name:  "github-bot",
				Forge: config.Forge{Type: "github", Host: "https://github.example", TokenEnv: fakeForgeToken},
				Repos: []config.Repo{{Owner: "beta", Name: "app"}},
			},
		},
	}

	view := BuildConfigView(ConfigViewInput{Config: cfg})

	if !view.MultiIdentity {
		t.Error("MultiIdentity = false with two configured identities; a reader would attribute every task to the default forge")
	}
	if len(view.Identities) != 2 {
		t.Fatalf("Identities = %+v, want both configured identities", view.Identities)
	}
	gitea := view.Identities[0]
	if gitea.Name != "gitea-bot" || gitea.ForgeType != "gitea" || gitea.ForgeHost != "https://gitea.example" {
		t.Errorf("Identities[0] = %+v, want the gitea-bot forge coordinates", gitea)
	}
	if len(gitea.Repos) != 2 || gitea.Repos[0].Owner != "acme" || gitea.Repos[0].Name != "gadget" || gitea.Repos[1].Name != "svc" {
		t.Errorf("Identities[0].Repos = %+v, want the repositories it owns (owner and name are what attributing a task needs)", gitea.Repos)
	}
	if view.Identities[1].Name != "github-bot" || view.Identities[1].ForgeHost != "https://github.example" {
		t.Errorf("Identities[1] = %+v, want the github-bot forge coordinates", view.Identities[1])
	}

	// The projection is the browser's copy, so the per-identity forges are a
	// new surface for a token to escape through.
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal view: %v", err)
	}
	for _, leak := range leakCandidates() {
		if strings.Contains(string(body), leak) {
			t.Errorf("published projection leaked %q; per-identity forges must publish coordinates only, never credentials", leak)
		}
	}
}

// TestConfigViewWithoutIdentitiesCarriesNone: a single-identity deployment
// publishes no identity list, and the default identity's forge in Identity
// stays the whole answer. This is the shape every deployment produces today.
func TestConfigViewWithoutIdentitiesCarriesNone(t *testing.T) {
	view := BuildConfigView(ConfigViewInput{Config: configWithFakeSecrets().Get()})

	if view.MultiIdentity {
		t.Error("MultiIdentity = true without [[identities]]; the projection would claim a per-identity layout it does not have")
	}
	if len(view.Identities) != 0 {
		t.Errorf("Identities = %+v, want none for a single-identity deployment", view.Identities)
	}
	if view.Identity.ForgeHost != "gitea.example.com" {
		t.Errorf("Identity.ForgeHost = %q, want the deployment's own forge", view.Identity.ForgeHost)
	}
}

func TestHandleConfigNeverLeaksSecrets(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Holder
	}{
		{name: "populated config with secrets", cfg: configWithFakeSecrets()},
		{name: "nil config", cfg: nil},
		{name: "zero-value config", cfg: config.NewHolder(config.Config{})},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			localConfig(srv, tc.cfg)

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body)
			}

			body := w.Body.String()
			for _, leak := range leakCandidates() {
				if strings.Contains(body, leak) {
					t.Errorf("response body leaked secret value %q:\n%s", leak, body)
				}
			}

			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
		})
	}
}

// TestHandleConfigSafeFieldsPresent proves the allowlisted, non-secret
// fields still make it through -- a test that only checked for absence of
// secrets could pass trivially by returning {}.
func TestHandleConfigSafeFieldsPresent(t *testing.T) {
	srv := newTestServer(t)
	localConfig(srv, configWithFakeSecrets())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.Identity.BotUser != "archie-bot" {
		t.Errorf("Identity.BotUser = %q, want archie-bot", got.Identity.BotUser)
	}
	if got.Identity.ForgeHost != "gitea.example.com" {
		t.Errorf("Identity.ForgeHost = %q, want gitea.example.com", got.Identity.ForgeHost)
	}
	if len(got.Repositories) != 1 || got.Repositories[0].Owner != "acme" {
		t.Errorf("Repositories = %+v", got.Repositories)
	}
	if !got.Providers["openai"].Configured {
		t.Errorf("Providers[openai].Configured = false, want true (APIKeyEnv is set)")
	}
	if got.Providers["openai"].APIKeyEnv != "OPENAI_API_KEY" {
		t.Errorf("Providers[openai].APIKeyEnv = %q, want OPENAI_API_KEY", got.Providers["openai"].APIKeyEnv)
	}
	if got.Storage.WorkDir != "/work/archie" {
		t.Errorf("Storage.WorkDir = %q", got.Storage.WorkDir)
	}
	if strings.Contains(w.Body.String(), `"agent"`) {
		t.Errorf("removed agent execution selector leaked through config API: %s", w.Body)
	}
	var raw struct {
		Containers map[string]any `json:"containers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw config view: %v", err)
	}
	if _, exists := raw.Containers["enabled"]; exists {
		t.Errorf("removed container execution switch leaked through config API: %s", w.Body)
	}
}

// TestHandleConfigIncludesReloadStatus proves a failed reload is surfaced
// in /api/config so the operator can see the running config is stale
// without reading logs.
func TestHandleConfigIncludesReloadStatus(t *testing.T) {
	srv := newTestServer(t)
	localView(srv, ConfigViewInput{
		Config: configWithFakeSecrets().Get(),
		Reload: &config.ReloadStatus{
			LastError:   "poll_interval must be positive",
			LastErrorAt: "2026-08-09T12:00:00Z",
		},
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Reload == nil {
		t.Fatal("Reload = nil, want the failed-reload status")
	}
	if got.Reload.LastError != "poll_interval must be positive" {
		t.Errorf("Reload.LastError = %q", got.Reload.LastError)
	}
	if got.Reload.LastErrorAt != "2026-08-09T12:00:00Z" {
		t.Errorf("Reload.LastErrorAt = %q", got.Reload.LastErrorAt)
	}
}

// TestHandleConfigOmitsReloadStatusWhenUnavailable pins that the reload
// field is absent (not empty) when the server has no reload seam wired.
func TestHandleConfigOmitsReloadStatusWhenUnavailable(t *testing.T) {
	srv := newTestServer(t)
	localConfig(srv, configWithFakeSecrets())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Reload != nil {
		t.Errorf("Reload = %+v, want nil (no reload seam)", got.Reload)
	}
}

// TestConfigViewCarriesProvenance proves the provenance list the
// configuration owner supplies reaches /api/config.
func TestConfigViewCarriesProvenance(t *testing.T) {
	srv := newTestServer(t)
	localView(srv, ConfigViewInput{
		Config: configWithFakeSecrets().Get(),
		Provenance: []ConfigOrigin{
			{Path: "/etc/archie/config.toml", Role: "main", Layer: "base"},
			{Path: "/etc/archie/conf.d/docker.yaml", Role: "extra", Layer: "overlay", Feature: "docker"},
		},
	})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Provenance) != 2 {
		t.Fatalf("Provenance = %+v, want 2 origins", got.Provenance)
	}
	if got.Provenance[0].Path != "/etc/archie/config.toml" {
		t.Errorf("Provenance[0].Path = %q", got.Provenance[0].Path)
	}
}

// TestHandleConfigReportsLockedKeys proves the dashboard is told which
// config keys it cannot edit and why, so it can disable those rows
// instead of silently omitting the edit affordance.
func TestHandleConfigReportsLockedKeys(t *testing.T) {
	srv := newTestServer(t)
	localConfig(srv, configWithFakeSecrets())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"db_path", "work_dir"} {
		if got.Locked[key] == "" {
			t.Errorf("Locked[%q] is empty, want a reason", key)
		}
	}
}

// TestHandleConfigRepoViewIncludesReviewEnabled proves Repo.ReviewEnabled
// reaches the dashboard alongside AllowConcurrent and MaxRetries --
// previously it was absent from RepoView entirely, not merely unrendered
// (archie-core-b6ew.4).
func TestHandleConfigRepoViewIncludesReviewEnabled(t *testing.T) {
	srv := newTestServer(t)
	localView(srv, ConfigViewInput{Config: config.Config{
		Repos: []config.Repo{
			{Owner: "acme", Name: "widget", Base: "main", AllowConcurrent: true, MaxRetries: 3, ReviewEnabled: true},
			{Owner: "acme", Name: "gadget", Base: "main"},
		},
	}})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Repositories) != 2 {
		t.Fatalf("Repositories = %+v, want 2", got.Repositories)
	}
	widget := got.Repositories[0]
	if !widget.AllowConcurrent || widget.MaxRetries != 3 || !widget.ReviewEnabled {
		t.Errorf("widget = %+v, want AllowConcurrent=true MaxRetries=3 ReviewEnabled=true", widget)
	}
	gadget := got.Repositories[1]
	if gadget.AllowConcurrent || gadget.MaxRetries != 0 || gadget.ReviewEnabled {
		t.Errorf("gadget = %+v, want all repo-tuning fields at their zero value", gadget)
	}
}

// TestHandleConfigIncludesSchemaWithLiveValues proves the schema (archie-core-b6ew.2)
// carries the same values as the flat ConfigView fields it is built from,
// not a stale or empty catalog.
func TestHandleConfigIncludesSchemaWithLiveValues(t *testing.T) {
	srv := newTestServer(t)
	localConfig(srv, configWithFakeSecrets())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Schema) == 0 {
		t.Fatal("Schema is empty, want the field descriptor catalog")
	}

	fields := map[string]ConfigField{}
	for _, section := range got.Schema {
		for _, f := range section.Fields {
			fields[f.Key] = f
		}
	}

	botUser, ok := fields["bot_user"]
	if !ok {
		t.Fatal(`Schema has no "bot_user" field`)
	}
	if botUser.Value != got.Identity.BotUser {
		t.Errorf("Schema[bot_user].Value = %v, want %v (ConfigView.Identity.BotUser)", botUser.Value, got.Identity.BotUser)
	}
	if !botUser.Editable {
		t.Error("Schema[bot_user].Editable = false, want true")
	}

	// A locked field's schema entry carries the reason the flat Locked map
	// carries, so the generic renderer does not need to cross-reference it.
	workDir, ok := fields["work_dir"]
	if !ok {
		t.Fatal(`Schema has no "work_dir" field`)
	}
	if workDir.LockedReason == "" {
		t.Error("Schema[work_dir].LockedReason is empty, want the overlay-denied reason")
	}
	if workDir.LockedReason != got.Locked["work_dir"] {
		t.Errorf("Schema[work_dir].LockedReason = %q, want %q (ConfigView.Locked[work_dir])", workDir.LockedReason, got.Locked["work_dir"])
	}

	// Structured fields still carry their value (for the dedicated editors
	// archie-core-b6ew.4 adds) but are not marked editable by the generic
	// scalar renderer.
	repos, ok := fields["repos"]
	if !ok {
		t.Fatal(`Schema has no "repos" field`)
	}
	if repos.Editable {
		t.Error("Schema[repos].Editable = true, want false (structured fields need a dedicated editor)")
	}
	reposValue, ok := repos.Value.([]any)
	if !ok || len(reposValue) != 1 {
		t.Errorf("Schema[repos].Value = %#v, want the one configured repository", repos.Value)
	}
}

// TestHandleConfigSchemaNeverLeaksSecrets extends the secret-leak guard to
// the schema block specifically, so a future field added to
// configFieldDescriptors without going through ConfigView's allowlist would
// be caught even if leakCandidates never appears in the flat fields.
func TestHandleConfigSchemaNeverLeaksSecrets(t *testing.T) {
	srv := newTestServer(t)
	localConfig(srv, configWithFakeSecrets())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	schemaJSON, err := json.Marshal(got.Schema)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	for _, leak := range leakCandidates() {
		if strings.Contains(string(schemaJSON), leak) {
			t.Errorf("schema leaked secret value %q:\n%s", leak, schemaJSON)
		}
	}
}

// TestHandleChannelsWithoutManagerIsEmpty: channel lifecycle is the status
// manager's to report. A process without one answers an empty list rather
// than deriving channels from configuration, which could only ever describe
// what was configured, never what is running.
func TestHandleChannelsWithoutManagerIsEmpty(t *testing.T) {
	srv := newTestServer(t)
	localConfig(srv, configWithFakeSecrets())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, leak := range leakCandidates() {
		if strings.Contains(body, leak) {
			t.Errorf("channels response leaked secret value %q:\n%s", leak, body)
		}
	}
	var got struct {
		Channels []ChannelView `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Channels) != 0 {
		t.Errorf("Channels = %+v, want empty without a status manager", got.Channels)
	}
}

func TestHandleChannelsUsesRuntimeManager(t *testing.T) {
	srv := newTestServer(t)
	srv.Channels = status.NewManager([]status.Descriptor{{
		ID: "telegram", Name: "Telegram", Configured: true, ReloadSupported: true,
	}})
	srv.Channels.MarkFailed("telegram", "token rejected")

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got struct {
		Channels []ChannelView `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Channels) != 1 || got.Channels[0].State != "failed" || !got.Channels[0].ReloadSupported {
		t.Fatalf("channels = %#v", got.Channels)
	}
}

// TestRemoteConfigViewRendersThePublishedSnapshot: the UI process has no
// configuration of its own, so the page it serves is whatever the owner
// published. Whether that page is editable is decided by the process that
// renders it, not by the document -- see
// TestConfigViewEditableFollowsThisProcessWritePath.
func TestRemoteConfigViewRendersThePublishedSnapshot(t *testing.T) {
	published := ConfigView{
		Identity:   IdentityView{BotUser: "archie", ForgeType: "github"},
		Models:     map[string]string{"chat": "anthropic/claude"},
		Providers:  map[string]ProviderView{"anthropic": {APIKeyEnv: "ANTHROPIC_API_KEY", Configured: true}},
		Provenance: []ConfigOrigin{{Path: "/etc/archie/config.toml", Role: "main"}},
	}
	document, err := json.Marshal(published)
	if err != nil {
		t.Fatal(err)
	}
	source := RemoteConfigView(stubSnapshots{snapshot: store.ConfigSnapshot{
		Schema:   ConfigViewSchema,
		Document: document,
	}, found: true})

	view, found, err := source(t.Context())
	if err != nil || !found {
		t.Fatalf("RemoteConfigView = (found %v, %v), want the published snapshot", found, err)
	}
	if view.Identity.BotUser != "archie" || view.Providers["anthropic"].APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Fatalf("view = %+v, want the published values", view)
	}
	if len(view.Provenance) != 1 || view.Provenance[0].Path != "/etc/archie/config.toml" {
		t.Errorf("Provenance = %+v, want the published chain", view.Provenance)
	}
}

// TestRemoteConfigViewRefusesAnUnknownSchema: a document whose shape this
// build does not know is not something to render half of.
func TestRemoteConfigViewRefusesAnUnknownSchema(t *testing.T) {
	source := RemoteConfigView(stubSnapshots{snapshot: store.ConfigSnapshot{
		Schema:   "webui.ConfigView/99",
		Document: []byte(`{}`),
	}, found: true})

	if _, found, err := source(t.Context()); err == nil || found {
		t.Fatalf("unknown schema = (found %v, %v), want an error", found, err)
	}
}

// TestConfigWithNoPublishedSnapshotIsEmpty: before the owner has published
// anything the page has nothing to show, which is the documented degraded
// response, not a failure.
func TestConfigWithNoPublishedSnapshotIsEmpty(t *testing.T) {
	srv := newTestServer(t)
	srv.ConfigSource = RemoteConfigView(stubSnapshots{})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "{}" {
		t.Fatalf("GET /api/config = %d %q, want 200 {}", res.Code, res.Body)
	}
}

type stubSnapshots struct {
	snapshot store.ConfigSnapshot
	found    bool
	err      error
}

func (s stubSnapshots) PutConfigSnapshot(context.Context, store.ConfigSnapshot) error { return s.err }

func (s stubSnapshots) ConfigSnapshot(context.Context) (store.ConfigSnapshot, bool, error) {
	return s.snapshot, s.found, s.err
}

// TestCapabilitiesReportWhatThisProcessCanServe: the same dashboard is served
// by the daemon, which holds every runtime handle, and by the UI process,
// which holds two remote contracts. A section with nothing behind it answers
// empty rather than failing, which the browser cannot tell from a quiet
// deployment -- so the server says which sections it can back.
func TestCapabilitiesReportWhatThisProcessCanServe(t *testing.T) {
	get := func(srv *Server) map[string]bool {
		t.Helper()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/capabilities", nil)
		res := httptest.NewRecorder()
		srv.Handler().ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET /api/capabilities = %d, want 200", res.Code)
		}
		var body struct {
			Sections map[string]bool `json:"sections"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode capabilities: %v", err)
		}
		return body.Sections
	}

	bare := get(newTestServer(t))
	for _, section := range []string{"logs", "curators", "channels", "mappings", "bindings", "skills"} {
		if bare[section] {
			t.Errorf("section %q reported available with nothing wired behind it", section)
		}
	}
	// The task board and configuration page work from the store and the
	// configured source, so they are never hidden.
	for _, section := range []string{"workflows", "settings"} {
		if !bare[section] {
			t.Errorf("section %q reported unavailable; it is served in every composition", section)
		}
	}

	wired := newTestServer(t)
	wired.Channels = status.NewManager([]status.Descriptor{{ID: "telegram", Name: "Telegram"}})
	wired.LogFeed = logging.NewFeed(10)
	wired.Curators = stubCurators{}
	wired.Skills = stubSkillCatalog{}
	got := get(wired)
	for _, section := range []string{"logs", "curators", "skills", "channels"} {
		if !got[section] {
			t.Errorf("section %q reported unavailable despite being wired", section)
		}
	}
}

// stubCurators satisfies the webui-owned CuratorStatus view for tests.
type stubCurators struct{}

func (stubCurators) Names() []string { return []string{"wired"} }
func (stubCurators) Health(_ context.Context) map[string]CuratorHealthView {
	return map[string]CuratorHealthView{"wired": {Status: "healthy"}}
}
func (stubCurators) Activity(string) (CuratorActivity, bool) { return CuratorActivity{}, false }

// stubSkillCatalog satisfies the webui-owned SkillCatalog view for tests.
type stubSkillCatalog struct{}

func (stubSkillCatalog) Skills() []SkillView { return []SkillView{{Name: "demo"}} }
