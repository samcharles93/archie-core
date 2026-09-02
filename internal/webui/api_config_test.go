package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/channels"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
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
			srv.Cfg = tc.cfg

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
	srv.Cfg = configWithFakeSecrets()

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
	srv.Cfg = configWithFakeSecrets()
	srv.LastReload = func() config.ReloadStatus {
		return config.ReloadStatus{
			LastError:   "poll_interval must be positive",
			LastErrorAt: "2026-08-09T12:00:00Z",
		}
	}

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
	srv.Cfg = configWithFakeSecrets()

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

// TestSetProvenancePublishesForConfigView proves a reloaded provenance
// list reaches /api/config.
func TestSetProvenancePublishesForConfigView(t *testing.T) {
	srv := newTestServer(t)
	srv.Cfg = configWithFakeSecrets()
	srv.SetProvenance([]ConfigOrigin{
		{Path: "/etc/archie/config.toml", Role: "main", Layer: "base"},
		{Path: "/etc/archie/conf.d/docker.yaml", Role: "extra", Layer: "overlay", Feature: "docker"},
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
	srv.Cfg = configWithFakeSecrets()

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
	srv.Cfg = config.NewHolder(config.Config{
		Repos: []config.Repo{
			{Owner: "acme", Name: "widget", Base: "main", AllowConcurrent: true, MaxRetries: 3, ReviewEnabled: true},
			{Owner: "acme", Name: "gadget", Base: "main"},
		},
	})

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
	srv.Cfg = configWithFakeSecrets()

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
	srv.Cfg = configWithFakeSecrets()

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

// TestHandleConfigUpdateAppliesViaSeam proves the PATCH handler passes
// the decoded updates to the wired UpdateConfig seam and answers ok.
func TestHandleConfigUpdateAppliesViaSeam(t *testing.T) {
	srv := newTestServer(t)
	var got map[string]any
	srv.UpdateConfig = func(_ context.Context, updates map[string]any) error {
		got = updates
		return nil
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/config",
		strings.NewReader(`{"updates": {"budgets.max_steps": 12}}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if got["budgets.max_steps"] != float64(12) {
		t.Errorf("updates = %#v, want budgets.max_steps=12", got)
	}
}

// TestHandleConfigUpdateInvalidMapsTo400 proves a rejected update (the
// seam wraps its error with ErrConfigUpdateInvalid) answers 400.
func TestHandleConfigUpdateInvalidMapsTo400(t *testing.T) {
	srv := newTestServer(t)
	srv.UpdateConfig = func(context.Context, map[string]any) error {
		return fmt.Errorf("%w: label is not runtime-tunable", ErrConfigUpdateInvalid)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/config",
		strings.NewReader(`{"updates": {"label": "x"}}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body)
	}
}

// TestHandleConfigUpdateUnavailableMapsTo503 proves a disabled overlay
// (ErrConfigUpdateUnavailable) answers 503, as does a server with no
// UpdateConfig seam at all.
func TestHandleConfigUpdateUnavailableMapsTo503(t *testing.T) {
	srv := newTestServer(t)
	srv.UpdateConfig = func(context.Context, map[string]any) error {
		return fmt.Errorf("%w: overlay disabled by recovery flag", ErrConfigUpdateUnavailable)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/config",
		strings.NewReader(`{"updates": {"label": "x"}}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", w.Code, w.Body)
	}
}

func TestHandleConfigUpdateWithoutSeamMapsTo503(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, "/api/config",
		strings.NewReader(`{"updates": {"label": "x"}}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", w.Code, w.Body)
	}
}

// TestHandleConfigIncludesOverridden proves the overridden dotted keys
// reach the view so the UI can mark rows shadowed by the runtime overlay.
func TestHandleConfigIncludesOverridden(t *testing.T) {
	srv := newTestServer(t)
	srv.Cfg = configWithFakeSecrets()
	srv.ConfigOverrides = func(context.Context) ([]string, error) {
		return []string{"budgets.max_steps", "label"}, nil
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/config", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got ConfigView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Overridden) != 2 || got.Overridden[0] != "budgets.max_steps" || got.Overridden[1] != "label" {
		t.Errorf("Overridden = %v, want [budgets.max_steps label]", got.Overridden)
	}
}

func TestHandleConfigResetCallsSeamAndAnswersOk(t *testing.T) {
	srv := newTestServer(t)
	var got string
	srv.ResetConfig = func(_ context.Context, key string) error {
		got = key
		return nil
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/reset",
		strings.NewReader(`{"key": "budgets.max_steps"}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	if got != "budgets.max_steps" {
		t.Errorf("ResetConfig called with %q, want budgets.max_steps", got)
	}
}

func TestHandleConfigResetWithoutSeamMapsTo503(t *testing.T) {
	srv := newTestServer(t)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/reset",
		strings.NewReader(`{"key": "label"}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", w.Code, w.Body)
	}
}

func TestHandleConfigResetRejectsEmptyKey(t *testing.T) {
	srv := newTestServer(t)
	srv.ResetConfig = func(context.Context, string) error { return nil }

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/config/reset",
		strings.NewReader(`{"key": ""}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", w.Code, w.Body)
	}
}

func TestHandleChannelsReportsConfiguredState(t *testing.T) {
	srv := newTestServer(t)
	srv.Cfg = configWithFakeSecrets()

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

	var telegram *ChannelView
	for i := range got.Channels {
		if got.Channels[i].Name == "Telegram" {
			telegram = &got.Channels[i]
		}
	}
	if telegram == nil {
		t.Fatal("no Telegram channel in response")
	}
	if !telegram.Configured {
		t.Errorf("Telegram.Configured = false, want true (TokenEnv is set)")
	}
}

func TestHandleChannelsNilConfig(t *testing.T) {
	srv := newTestServer(t)
	srv.Cfg = nil

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/channels", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	var got struct {
		Channels []ChannelView `json:"channels"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Channels) != 0 {
		t.Errorf("Channels = %+v, want empty when Cfg is nil", got.Channels)
	}
}

func TestHandleChannelsUsesRuntimeManager(t *testing.T) {
	srv := newTestServer(t)
	srv.Cfg = configWithFakeSecrets()
	srv.Channels = channels.NewManager([]channels.Descriptor{{
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
