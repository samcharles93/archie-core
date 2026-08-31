package archied

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	natssrv "github.com/nats-io/nats-server/v2/test"
	natsio "github.com/nats-io/nats.go"
	"github.com/samcharles93/ai-sdk/chat"

	"github.com/samcharles93/archie-core/internal/agentexec"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/events"
	"github.com/samcharles93/archie-core/internal/forge"
	"github.com/samcharles93/archie-core/internal/forgerpc"
	"github.com/samcharles93/archie-core/internal/gateway"
	"github.com/samcharles93/archie-core/internal/logging"
	"github.com/samcharles93/archie-core/internal/secret"
	"github.com/samcharles93/archie-core/internal/store"
	"github.com/samcharles93/archie-core/internal/storerpc"
	"github.com/samcharles93/archie-core/internal/tools"
	"github.com/samcharles93/archie-core/internal/worktree"
	"github.com/samcharles93/archie-core/internal/worktreerpc"
)

// recordingHandler captures warning messages so tests can distinguish the
// silent disabled-forge path from the token-unavailable and
// client-construction-failed fallbacks, which both log before returning the
// same noop forge.
type recordingHandler struct {
	mu    sync.Mutex
	warns []string
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r.Level == slog.LevelWarn {
		h.warns = append(h.warns, r.Message)
	}
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// Warnings returns a copy of the captured warning messages.
func (h *recordingHandler) Warnings() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.warns...)
}

// TestResolveForgeDegradesInsteadOfFailing guards a startup behaviour change:
// a missing forge credential used to abort archied with exit 1. The forge is
// one feature among many, so that denied the operator chat, the gateway and
// every other subsystem -- and under the installed systemd unit
// (Restart=on-failure, RestartSec=5s) it crash-looped where the error was
// never read. A missing credential must disable the forge, not the daemon.
func TestResolveForgeDegradesInsteadOfFailing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.Forge
		// wantWarnSubstring discriminates the disabled path from the
		// fallback paths: a type ForgeDisabled accepts returns the noop forge
		// silently, while a token that cannot resolve or a forge that cannot
		// be constructed logs a warning first. Without this assertion the
		// alias cases are indistinguishable from the fallbacks -- a future
		// copy of the predicate that only recognized "none" would pass.
		wantWarnSubstring string
	}{
		{
			name: "explicitly disabled",
			cfg:  config.Forge{Type: "none"},
		},
		{
			name: "off alias",
			cfg:  config.Forge{Type: "off"},
		},
		{
			name: "disabled alias",
			cfg:  config.Forge{Type: "disabled"},
		},
		{
			name: "token refers to an unset environment variable",
			cfg: config.Forge{
				Type:  "github",
				Host:  "https://github.com",
				Token: secret.SecretRef{Engine: "env", Key: "ARCHIE_TEST_TOKEN_DEFINITELY_UNSET"},
			},
			wantWarnSubstring: "token unavailable",
		},
		{
			name: "token reference is empty",
			cfg:  config.Forge{Type: "github", Host: "https://github.com"},
			// The zero-value SecretRef resolves to an empty token, which hits
			// the token-unavailable branch -- but only after logging. This
			// stays discriminating: the disabled aliases return silently.
			wantWarnSubstring: "token unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := &recordingHandler{}
			client, token := resolveForge(tc.cfg, secret.NewRegistry(), slog.New(rec))
			if client == nil {
				t.Fatal("resolveForge returned no forge client; startup would nil-panic")
			}
			if token != "" {
				t.Errorf("token = %q, want empty when the forge is disabled", token)
			}
			// The no-op forge answers rather than erroring, which is what lets
			// the rest of the daemon run against it unchanged.
			issues, err := client.IssuesWithLabel(t.Context(), "owner", "repo", "archie")
			if err != nil {
				t.Errorf("disabled forge returned an error instead of no work: %v", err)
			}
			if len(issues) != 0 {
				t.Errorf("disabled forge returned %d issues, want 0", len(issues))
			}
			if tc.wantWarnSubstring == "" {
				if warns := rec.Warnings(); len(warns) != 0 {
					t.Errorf("disabled alias logged %d warning(s), want none: %v", len(warns), warns)
				}
				return
			}
			warns := rec.Warnings()
			if len(warns) != 1 || !strings.Contains(warns[0], tc.wantWarnSubstring) {
				t.Errorf("warnings = %v, want exactly one containing %q", warns, tc.wantWarnSubstring)
			}
		})
	}
}

// Not parallel: t.Setenv mutates process state and panics under t.Parallel.
func TestResolveForgeBuildsClientWhenTokenResolves(t *testing.T) {
	t.Setenv("ARCHIE_TEST_FORGE_TOKEN", "ghp_exampletoken")
	log := slog.New(slog.DiscardHandler)

	client, token := resolveForge(config.Forge{
		Type:  "github",
		Host:  "https://github.com",
		Token: secret.SecretRef{Engine: "env", Key: "ARCHIE_TEST_FORGE_TOKEN"},
	}, secret.NewRegistry(), log)

	if client == nil {
		t.Fatal("resolveForge returned no forge client")
	}
	if token != "ghp_exampletoken" {
		t.Errorf("token = %q, want the resolved value passed to the worktree manager", token)
	}
}

func TestResolveProviderSecretSetsPrivateRuntimeEnvironment(t *testing.T) {
	registry := secret.NewRegistry()
	registry.Register(providerSecretEngine{value: "provider-secret"})
	cfg := config.Config{Providers: map[string]config.Provider{
		"openai": {
			Class:  "openai",
			APIKey: secret.SecretRef{Engine: "provider-test", Key: "OPENAI_API_KEY"},
		},
	}}

	if err := resolveProviderSecrets(&cfg, registry); err != nil {
		t.Fatal(err)
	}
	provider := cfg.Providers["openai"]
	if provider.APIKeyEnv == "" {
		t.Fatal("resolved provider has no runtime environment name")
	}
	if provider.APIKey != (secret.SecretRef{}) {
		t.Fatal("resolved provider retained serializable secret reference")
	}
	if got := os.Getenv(provider.APIKeyEnv); got != "provider-secret" {
		t.Fatalf("runtime environment value = %q", got)
	}
	t.Cleanup(func() { _ = os.Unsetenv(provider.APIKeyEnv) })
}

func TestResolveProviderSecretsIsolatesIdentityCredentials(t *testing.T) {
	registry := secret.NewRegistry()
	registry.Register(mappedProviderSecretEngine{values: map[string]string{
		"root-key": "root-secret", "one-key": "one-secret", "two-key": "two-secret",
	}})
	cfg := config.Config{
		Providers: map[string]config.Provider{"openai": {
			APIKey: secret.SecretRef{Engine: "provider-map", Key: "root-key"},
		}},
		Identities: []config.IdentityConfig{
			{Name: "one", Providers: map[string]config.Provider{"openai": {
				APIKey: secret.SecretRef{Engine: "provider-map", Key: "one-key"},
			}}},
			{Name: "two", Providers: map[string]config.Provider{"openai": {
				APIKey: secret.SecretRef{Engine: "provider-map", Key: "two-key"},
			}}},
		},
	}

	if err := resolveProviderSecrets(&cfg, registry); err != nil {
		t.Fatal(err)
	}
	names := []string{
		cfg.Providers["openai"].APIKeyEnv,
		cfg.Identities[0].Providers["openai"].APIKeyEnv,
		cfg.Identities[1].Providers["openai"].APIKeyEnv,
	}
	if names[0] == names[1] || names[0] == names[2] || names[1] == names[2] {
		t.Fatalf("credential environment names collide: %v", names)
	}
	for i, want := range []string{"root-secret", "one-secret", "two-secret"} {
		if got := os.Getenv(names[i]); got != want {
			t.Errorf("credential %d = %q, want %q", i, got, want)
		}
		t.Cleanup(func() { _ = os.Unsetenv(names[i]) })
	}
}

type providerSecretEngine struct{ value string }

func (providerSecretEngine) Name() string    { return "provider-test" }
func (providerSecretEngine) Version() string { return "test" }
func (e providerSecretEngine) Resolve(string) (string, error) {
	return e.value, nil
}

type mappedProviderSecretEngine struct{ values map[string]string }

func (mappedProviderSecretEngine) Name() string    { return "provider-map" }
func (mappedProviderSecretEngine) Version() string { return "test" }
func (e mappedProviderSecretEngine) Resolve(key string) (string, error) {
	value, ok := e.values[key]
	if !ok {
		return "", fmt.Errorf("missing test secret")
	}
	return value, nil
}

func TestChatGenerateOptionsIncludesToolsAndMultipleSteps(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.ToolEntry{
		Name:    "memory_search",
		Handler: func(context.Context, map[string]any) (any, error) { return "found", nil },
	}); err != nil {
		t.Fatal(err)
	}
	messages := []chat.Message{{Role: chat.RoleUser, Content: "remember this"}}

	options, err := chatGenerateOptions(context.Background(), messages, registry, 0, agentexec.ToolLimits{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if options.MaxSteps != defaultChatMaxSteps || options.MaxSteps <= 1 {
		t.Fatalf("MaxSteps = %d, want %d and greater than one", options.MaxSteps, defaultChatMaxSteps)
	}
	if len(options.Tools) != 1 || options.Tools["memory_search"].Execute == nil {
		t.Fatalf("Tools = %#v, want executable memory_search", options.Tools)
	}
	if len(options.Messages) != 1 || options.Messages[0].Content != messages[0].Content {
		t.Fatalf("Messages = %#v, want input preserved", options.Messages)
	}
}

func TestChatGenerateOptionsReportsInvalidToolSchemas(t *testing.T) {
	registry := tools.NewRegistry()
	if err := registry.Register(tools.ToolEntry{
		Name:    "invalid",
		Handler: func(context.Context, map[string]any) (any, error) { return nil, nil },
		DynamicSchemaOverrides: func(tools.JSONSchema) tools.JSONSchema {
			panic("schema panic")
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := chatGenerateOptions(context.Background(), nil, registry, 0, agentexec.ToolLimits{}, nil); err == nil {
		t.Fatal("chatGenerateOptions(context.Background(),) error = nil, want invalid tool schema error")
	}
}

// TestChatGenerateOptionsHonoursConfiguredMaxSteps keeps the step budget
// deployment-controlled. A cap that cannot be raised without a rebuild is
// the situation this replaced.
func TestChatGenerateOptionsHonoursConfiguredMaxSteps(t *testing.T) {
	options, err := chatGenerateOptions(context.Background(), nil, tools.NewRegistry(), 250, agentexec.ToolLimits{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if options.MaxSteps != 250 {
		t.Errorf("MaxSteps = %d, want the configured 250", options.MaxSteps)
	}
}

func TestConfiguredMCPProviderSupportsAllTransportTypes(t *testing.T) {
	tests := []struct {
		name    string
		server  config.MCPServer
		wantID  string
		wantErr bool
	}{
		{
			name:   "explicit stdio",
			server: config.MCPServer{Name: "Git Hub", Transport: "stdio", Command: "mcp-github"},
			wantID: "mcp.git-hub",
		},
		{
			name:   "empty transport defaults to stdio",
			server: config.MCPServer{Name: "local", Command: "mcp-local"},
			wantID: "mcp.local",
		},
		{
			name:   "http transport creates HTTPTransport",
			server: config.MCPServer{Name: "remote", Transport: "http", URL: "https://example.invalid"},
			wantID: "mcp.remote",
		},
		{
			name:   "sse transport creates SSETransport",
			server: config.MCPServer{Name: "sse-server", Transport: "sse", SSEEndpoint: "https://example.invalid/sse"},
			wantID: "mcp.sse-server",
		},
		{
			name:    "unknown transport",
			server:  config.MCPServer{Name: "unknown", Transport: "magic", Command: "mcp"},
			wantErr: true,
		},
		{
			name:    "missing name",
			server:  config.MCPServer{Transport: "stdio", Command: "mcp"},
			wantErr: true,
		},
		{
			name:    "missing command for stdio",
			server:  config.MCPServer{Name: "missing", Transport: "stdio"},
			wantErr: true,
		},
		{
			name:    "missing url for http",
			server:  config.MCPServer{Name: "http-missing", Transport: "http"},
			wantErr: true,
		},
		{
			name:    "missing sse_endpoint for sse",
			server:  config.MCPServer{Name: "sse-missing", Transport: "sse"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := configuredMCPProvider(tt.server, t.TempDir())
			if (err != nil) != tt.wantErr {
				t.Fatalf("configuredMCPProvider() error = %v, wantErr %t", err, tt.wantErr)
			}
			if tt.wantErr {
				if provider != nil {
					t.Fatalf("provider = %T, want nil on error", provider)
				}
				return
			}
			if got := provider.Manifest().ID; got != tt.wantID {
				t.Fatalf("Manifest().ID = %q, want %q", got, tt.wantID)
			}
		})
	}
}

// TestNpmCacheServerEnvPersistsCacheAcrossRestarts: config.docker.toml
// launched an MCP server with `npx -y @pkg@version`, and the container has
// no persistent npm cache, so every daemon restart re-resolved and
// re-downloaded the package (and its dependency tree) from the registry --
// slow, and it turns a registry outage or yanked version into a daemon
// outage. It amplified the crash loop observed on 2026-07-30, since each
// restart repeated the whole download. Pointing NPM_CONFIG_CACHE at the
// daemon's own persistent work dir means the second and later starts hit a
// warm cache regardless of whether the daemon itself runs in an ephemeral
// container. NPM_CONFIG_PREFER_OFFLINE keeps npm from making a registry
// round trip at all when the cache already has what it needs.
func TestNpmCacheServerEnvPersistsCacheAcrossRestarts(t *testing.T) {
	cases := []struct {
		name    string
		command string
		workDir string
		want    []string
	}{
		{
			name:    "npx gets a persistent cache under the daemon's work dir",
			command: "npx",
			workDir: "/var/lib/archie",
			want: []string{
				"NPM_CONFIG_CACHE=/var/lib/archie/mcp-npm-cache",
				"NPM_CONFIG_PREFER_OFFLINE=true",
			},
		},
		{
			name:    "a non-npx command has no npm registry dependency to fix",
			command: "/usr/local/bin/my-mcp-server",
			workDir: "/var/lib/archie",
			want:    nil,
		},
		{
			name:    "empty command is left alone (configuredMCPProvider rejects it separately)",
			command: "",
			workDir: "/var/lib/archie",
			want:    nil,
		},
		{
			// This repo's own config.example.toml uses absolute paths for
			// other daemon-launched commands (update_check_command,
			// update_install_command), so an operator pinning npx the same
			// way must still get the fix -- an exact "npx" match would
			// silently skip it with no error, leaving the original bug in
			// place unnoticed.
			name:    "an absolute path to npx still gets the persistent cache",
			command: "/usr/local/bin/npx",
			workDir: "/var/lib/archie",
			want: []string{
				"NPM_CONFIG_CACHE=/var/lib/archie/mcp-npm-cache",
				"NPM_CONFIG_PREFER_OFFLINE=true",
			},
		},
		{
			// workDir is unconditionally defaulted before this ever runs
			// in production, but filepath.Join silently accepts an empty
			// string -- defend explicitly rather than trust that
			// invariant here too, matching memoryProvider's own guard
			// against the same possibility.
			name:    "an empty workDir is refused rather than caching into the daemon's CWD",
			command: "npx",
			workDir: "",
			want:    nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := npmCacheServerEnv(c.command, c.workDir)
			if !slices.Equal(got, c.want) {
				t.Errorf("npmCacheServerEnv(%q, %q) = %v, want %v", c.command, c.workDir, got, c.want)
			}
		})
	}
}

func TestManualRequeueTaskUsesPersistedStatus(t *testing.T) {
	tests := []string{store.StatusParked, store.StatusWaitingHuman}
	for index, status := range tests {
		t.Run(status, func(t *testing.T) {
			st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			if _, err := st.EnqueueIssue(t.Context(), "acme", "widget", index+1, "task", "", "", ""); err != nil {
				t.Fatal(err)
			}
			task, err := st.TaskByIssue(t.Context(), "acme", "widget", index+1)
			if err != nil || task == nil {
				t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
			}
			if err := st.Transition(t.Context(), task.ID, store.StatusQueued, status, ""); err != nil {
				t.Fatal(err)
			}
			if err := manualRequeueTask(t.Context(), st, task.ID); err != nil {
				t.Fatal(err)
			}
			got, err := st.TaskByID(t.Context(), task.ID)
			if err != nil || got == nil || got.Status != store.StatusQueued {
				t.Fatalf("TaskByID after requeue = (%+v, %v), want queued", got, err)
			}
		})
	}
}

func TestManualRequeueTaskRejectsOtherStatuses(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.EnqueueIssue(t.Context(), "acme", "widget", 1, "task", "", "", ""); err != nil {
		t.Fatal(err)
	}
	task, err := st.TaskByIssue(t.Context(), "acme", "widget", 1)
	if err != nil || task == nil {
		t.Fatalf("TaskByIssue = (%+v, %v)", task, err)
	}
	if err := manualRequeueTask(t.Context(), st, task.ID); err == nil {
		t.Fatal("manualRequeueTask accepted a queued task")
	}
}

func TestChatTaskListerAdapterReadsSQLiteIdentity(t *testing.T) {
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	want, err := st.EnqueueChatTask(t.Context(), "acme", "widget", "identity task", "", "tdd", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	lister := chatTaskListerAdapter{tasks: st.Tasks}
	got, err := lister.ListChatTasks(t.Context(), "reviewer", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != want.ID || got[0].Title != want.Title {
		t.Fatalf("ListChatTasks = %+v, want task %d", got, want.ID)
	}
}

func TestChatTaskWriterAdapter(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("enqueue failed")
	tests := []struct {
		name    string
		result  *store.Task
		err     error
		wantID  int64
		wantErr bool
	}{
		{name: "returns task id", result: &store.Task{ID: 42}, wantID: 42},
		{name: "propagates error", err: sentinel, wantErr: true},
		{name: "rejects nil task", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := chatTaskWriterAdapter{
				enqueue: func(
					context.Context,
					string, string, string, string, string, string,
				) (*store.Task, error) {
					return tt.result, tt.err
				},
			}
			id, err := adapter.EnqueueChatTask(
				context.Background(),
				"owner", "repo", "title", "body", "workflow", "identity",
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("EnqueueChatTask() error = %v, wantErr %t", err, tt.wantErr)
			}
			if id != tt.wantID {
				t.Fatalf("EnqueueChatTask() id = %d, want %d", id, tt.wantID)
			}
		})
	}
}

func TestChatTaskProfilesUsesIdentityRepositories(t *testing.T) {
	cfg := config.Config{
		Repos: []config.Repo{{Owner: "legacy", Name: "ignored"}},
		Identities: []config.IdentityConfig{
			{Name: "builder", Repos: []config.Repo{{Owner: "acme", Name: "archie-core"}}},
			{Name: "reviewer", Repos: []config.Repo{
				{Owner: "acme", Name: "example-service"},
				{Owner: "acme", Name: "ai-sdk"},
			}},
		},
	}

	profiles, defaultIdentity := chatTaskProfiles(cfg)
	if defaultIdentity != "builder" {
		t.Fatalf("default identity = %q, want builder", defaultIdentity)
	}
	if len(profiles) != 2 {
		t.Fatalf("profiles = %d, want 2", len(profiles))
	}
	if profiles[1].Identity != "reviewer" || profiles[1].DefaultOwner != "acme" ||
		profiles[1].DefaultRepo != "example-service" ||
		len(profiles[1].Repos) != 2 || profiles[1].Repos[1] != "acme/ai-sdk" {
		t.Errorf("reviewer profile = %+v", profiles[1])
	}
}

func TestChatTaskControllerAdapterRejectsForgeTask(t *testing.T) {
	adapter := chatTaskControllerAdapter{
		taskByID: func(context.Context, int64) (*store.Task, error) {
			return &store.Task{ID: 42, Source: store.SourceForge}, nil
		},
	}

	_, ok, err := adapter.ChatTaskStatus(context.Background(), 42)
	if err == nil || ok || !strings.Contains(err.Error(), "not chat-originated") {
		t.Fatalf("ChatTaskStatus() = (_, %t, %v), want rejected forge task", ok, err)
	}
}

func TestChatTaskControllerAdapterTransitions(t *testing.T) {
	task := &store.Task{ID: 42, Source: store.SourceChat, Status: store.StatusWaitingHuman}
	var requeueFrom, requeueWorkflow, transitionFrom, transitionTo, transitionDetail string
	adapter := chatTaskControllerAdapter{
		taskByID: func(context.Context, int64) (*store.Task, error) { return task, nil },
		requeue: func(_ context.Context, _ int64, from, workflow string) error {
			requeueFrom, requeueWorkflow = from, workflow
			return nil
		},
		transition: func(_ context.Context, _ int64, from, to, detail string) error {
			transitionFrom, transitionTo, transitionDetail = from, to, detail
			return nil
		},
	}

	if err := adapter.ApproveChatTask(context.Background(), task.ID); err != nil {
		t.Fatalf("ApproveChatTask(): %v", err)
	}
	if requeueFrom != store.StatusWaitingHuman || requeueWorkflow != "implement" {
		t.Errorf("requeue = %q/%q, want waiting_human/implement", requeueFrom, requeueWorkflow)
	}
	if err := adapter.CancelChatTask(context.Background(), task.ID, "cancelled by test"); err != nil {
		t.Fatalf("CancelChatTask(): %v", err)
	}
	// Declining from chat lands in the same state as declining from the
	// dashboard. It used to record StatusRejected, which the PR reconciler
	// also uses for "the pull request was closed without merging", so the
	// state could not tell an operator's decision from a forge outcome.
	if transitionFrom != store.StatusWaitingHuman || transitionTo != store.StatusClosedWontDo ||
		transitionDetail != "cancelled by test" {
		t.Errorf("transition = %q/%q/%q", transitionFrom, transitionTo, transitionDetail)
	}
}

func TestChatTaskLogReaderAdapterIdentityEnforcement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity string
		task     *store.Task
		wantErr  bool
	}{
		{
			name:     "own chat task allowed",
			identity: "archie",
			task:     &store.Task{ID: 42, Identity: "archie", Attempt: 1, Source: store.SourceChat},
			wantErr:  false,
		},
		{
			name:     "other identity chat task denied",
			identity: "archie",
			task:     &store.Task{ID: 42, Identity: "other-bot", Attempt: 1, Source: store.SourceChat},
			wantErr:  true,
		},
		{
			name:     "forge task (empty identity) denied",
			identity: "archie",
			task:     &store.Task{ID: 42, Identity: "", Attempt: 1, Source: store.SourceForge},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adapter := chatTaskLogReaderAdapter{
				tasks: func(_ context.Context, _ int64) (*store.Task, error) { return tt.task, nil },
			}
			_, err := adapter.ReadChatTaskLogs(context.Background(), tt.identity, tt.task.ID, 0, gateway.ChatTaskLogQuery{})
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestChatTaskLogReaderAdapterDefaultAttempt(t *testing.T) {
	task := &store.Task{ID: 42, Identity: "archie", Attempt: 3, Source: store.SourceChat}
	adapter := chatTaskLogReaderAdapter{
		tasks: func(_ context.Context, _ int64) (*store.Task, error) { return task, nil },
	}

	result, err := adapter.ReadChatTaskLogs(context.Background(), "archie", task.ID, 0, gateway.ChatTaskLogQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Attempt != 3 {
		t.Errorf("attempt = %d, want 3 (resolved from task.Attempt when 0 is passed)", result.Attempt)
	}
}

func TestChatTaskLogReaderAdapterEmptyResultWhenNoRegistry(t *testing.T) {
	task := &store.Task{ID: 42, Identity: "archie", Attempt: 1, Source: store.SourceChat}
	adapter := chatTaskLogReaderAdapter{
		tasks:    func(_ context.Context, _ int64) (*store.Task, error) { return task, nil },
		taskLogs: nil,
	}

	result, err := adapter.ReadChatTaskLogs(context.Background(), "archie", task.ID, 1, gateway.ChatTaskLogQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Entries == nil {
		t.Error("Entries is nil, want empty slice (non-nil avoids model retrying)")
	}
	if len(result.Entries) != 0 {
		t.Errorf("Entries = %+v, want empty", result.Entries)
	}
}

func TestChatTaskLogReaderAdapterRoundTrip(t *testing.T) {
	baseDir := t.TempDir()
	taskLogs := logging.NewTaskRegistry(baseDir, logging.NewFeed(10), logging.TaskSinkOptions{})
	t.Cleanup(func() { _ = taskLogs.Remove(42) })

	task := &store.Task{ID: 42, Identity: "archie", Attempt: 1, Source: store.SourceChat}
	adapter := chatTaskLogReaderAdapter{
		tasks:    func(_ context.Context, _ int64) (*store.Task, error) { return task, nil },
		taskLogs: taskLogs,
	}

	// Write through the registry (same path the daemon's NATS consumer takes).
	if err := taskLogs.Open(task.ID, task.Attempt); err != nil {
		t.Fatal(err)
	}
	taskLogs.Write(task.ID, logging.Entry{Level: "ERROR", Message: "gate failed", Fields: map[string]any{"component": "gate"}})
	if err := taskLogs.Close(task.ID); err != nil {
		t.Fatal(err)
	}

	// Read back through the adapter (same path the chat tool takes).
	result, err := adapter.ReadChatTaskLogs(context.Background(), "archie", task.ID, 1, gateway.ChatTaskLogQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(result.Entries))
	}
	entry := result.Entries[0]
	if entry.Message != "gate failed" || entry.Level != "ERROR" {
		t.Errorf("entry = %+v, want message=gate failed level=ERROR", entry)
	}
}

func TestChatTaskCommandsEndToEnd(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	profiles, defaultIdentity := chatTaskProfiles(config.Config{
		Identities: []config.IdentityConfig{{
			Name:  "reviewer",
			Repos: []config.Repo{{Owner: "acme", Name: "archie-core"}},
		}},
	})
	creator := gateway.NewStoreTaskCreatorForProfiles(
		chatTaskWriterAdapter{enqueue: st.EnqueueChatTask},
		profiles,
	)
	controller := gateway.NewStoreTaskController(chatTaskControllerAdapter{
		taskByID:   st.TaskByID,
		requeue:    st.Requeue,
		transition: st.Transition,
	})
	router := gateway.NewRouter(st, nil, "test")
	configureTaskCommands(router, creator, controller, defaultIdentity)

	reply, err := router.Route(ctx, gateway.Message{
		Text: "/spawn identity=reviewer workflow=feasibility Design native tasks",
	})
	if err != nil {
		t.Fatal(err)
	}
	var taskID int64
	if _, err := fmt.Sscanf(reply, "Created task %d:", &taskID); err != nil || taskID == 0 {
		t.Fatalf("spawn reply = %q, parse error = %v", reply, err)
	}
	task, err := st.TaskByID(ctx, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil || task.Source != store.SourceChat || task.Identity != "reviewer" ||
		task.Owner != "acme" || task.Repo != "archie-core" ||
		task.Workflow != "feasibility" {
		t.Fatalf("spawned task = %+v", task)
	}

	if err := st.Transition(ctx, taskID, store.StatusQueued, store.StatusWaitingHuman, "await approval"); err != nil {
		t.Fatal(err)
	}
	task, err = st.TaskByID(ctx, taskID)
	if err != nil || task == nil || task.Status != store.StatusWaitingHuman {
		t.Fatalf("waiting task = (%+v, %v)", task, err)
	}
	reply, err = router.Route(ctx, gateway.Message{Text: fmt.Sprintf("/approve identity=reviewer %d", taskID)})
	if err != nil || !strings.Contains(reply, "approved") {
		t.Fatalf("approve = (%q, %v)", reply, err)
	}
	task, err = st.TaskByID(ctx, taskID)
	if err != nil || task == nil || task.Status != store.StatusQueued || task.Workflow != "implement" {
		t.Fatalf("approved task = (%+v, %v)", task, err)
	}

	reply, err = router.Route(ctx, gateway.Message{Text: "/spawn identity=reviewer Cancel me"})
	if err != nil {
		t.Fatal(err)
	}
	var cancelID int64
	if _, err := fmt.Sscanf(reply, "Created task %d:", &cancelID); err != nil {
		t.Fatalf("cancel spawn reply = %q, parse error = %v", reply, err)
	}
	reply, err = router.Route(ctx, gateway.Message{Text: fmt.Sprintf("/cancel identity=builder %d", cancelID)})
	if err != nil || !strings.Contains(reply, "different identity") {
		t.Fatalf("cross-identity cancel = (%q, %v)", reply, err)
	}
	reply, err = router.Route(ctx, gateway.Message{Text: fmt.Sprintf("/cancel identity=reviewer %d", cancelID)})
	if err != nil || !strings.Contains(reply, "cancelled") {
		t.Fatalf("cancel = (%q, %v)", reply, err)
	}
	// Same terminal state as the dashboard's Reject: one decision, one state.
	cancelled, err := st.TaskByID(ctx, cancelID)
	if err != nil || cancelled == nil || cancelled.Status != store.StatusClosedWontDo {
		t.Fatalf("cancelled task = (%+v, %v)", cancelled, err)
	}
}

// stubForge implements forge.Forge with no-op successes for the four
// methods registerTaskRPCServers needs to prove reachable; every other
// method panics if called.
type stubForge struct{}

func (stubForge) Comment(context.Context, string, string, int, string) (int64, error) { return 1, nil }
func (stubForge) CloseIssue(context.Context, string, string, int, string) error       { return nil }
func (stubForge) CreatePR(context.Context, string, string, string, string, string, string) (int, error) {
	return 1, nil
}
func (stubForge) SetStateLabel(context.Context, string, string, int, string, []string) {}
func (stubForge) LinkBranch(context.Context, string, string, int, string) error        { return nil }

func (stubForge) AcceptInvitations(context.Context) error { panic("unexpected call") }

func (stubForge) AssignedIssues(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (stubForge) IssuesWithLabel(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected call")
}

func (stubForge) RepliesAfter(context.Context, string, string, int, int64, string) ([]forge.Reply, error) {
	panic("unexpected call")
}

func (stubForge) PRState(context.Context, string, string, int) (string, error) {
	panic("unexpected call")
}

func (stubForge) CreateIssue(context.Context, string, string, string, string, []string) (int, error) {
	panic("unexpected call")
}

func (stubForge) React(context.Context, string, string, int, string) error { panic("unexpected call") }

func (stubForge) VerifyPush(context.Context, string, string) error { panic("unexpected call") }

func startEmbeddedNATS(t *testing.T) *server.Server {
	t.Helper()
	srv := natssrv.RunRandClientPortServer()
	t.Cleanup(srv.Shutdown)
	return srv
}

func TestRegisterTaskRPCServersReachableFromClient(t *testing.T) {
	srv := startEmbeddedNATS(t)
	url := srv.ClientURL()

	serverConn, err := natsio.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(serverConn.Close)

	st := store.OpenTest(t)
	trees := &worktree.Manager{WorkDir: t.TempDir()}
	log := slog.New(slog.DiscardHandler)

	unsubscribe, err := registerTaskRPCServers(serverConn, st, stubForge{}, trees, nil, worktreerpc.NewGrants(), log)
	if err != nil {
		t.Fatalf("registerTaskRPCServers: %v", err)
	}
	t.Cleanup(unsubscribe)

	clientConn, err := natsio.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(clientConn.Close)

	ctx := context.Background()

	storeClient := &storerpc.Client{Conn: clientConn, Timeout: 2 * time.Second}
	if _, err := st.EnqueueIssue(ctx, "acme", "widget", 1, "t", "b", "", ""); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	task, err := st.ClaimNext(ctx)
	if err != nil || task == nil {
		t.Fatalf("claim: (%v, %v)", task, err)
	}
	if err := storeClient.Transition(ctx, task.ID, store.StatusRunning, store.StatusPROpen, "opened"); err != nil {
		t.Fatalf("storerpc Transition unreachable: %v", err)
	}

	forgeClient := &forgerpc.Client{Conn: clientConn, Timeout: 2 * time.Second}
	if _, err := forgeClient.Comment(ctx, "acme", "widget", 1, "hi"); err != nil {
		t.Fatalf("forgerpc Comment unreachable: %v", err)
	}

	treesClient := &worktreerpc.Client{Conn: clientConn, Timeout: 2 * time.Second}
	if err := treesClient.Push(ctx); err == nil {
		t.Fatal("expected Push without a dispatch grant to fail, proving the RPC round-tripped")
	} else if err.Error() == "" {
		t.Fatal("expected a non-empty error from the worktreerpc round trip")
	}
}

// identityForge records which forge instance served a CloseIssue call, so
// tests can prove identity-scoped RPC routing reaches the right forge.
type identityForge struct {
	closes int
}

func (*identityForge) Comment(context.Context, string, string, int, string) (int64, error) {
	return 1, nil
}

func (f *identityForge) CloseIssue(context.Context, string, string, int, string) error {
	f.closes++
	return nil
}

func (*identityForge) CreatePR(context.Context, string, string, string, string, string, string) (int, error) {
	return 1, nil
}
func (*identityForge) SetStateLabel(context.Context, string, string, int, string, []string) {}
func (*identityForge) LinkBranch(context.Context, string, string, int, string) error        { return nil }

func (*identityForge) AcceptInvitations(context.Context) error { panic("unexpected") }

func (*identityForge) AssignedIssues(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected")
}

func (*identityForge) IssuesWithLabel(context.Context, string, string, string) ([]forge.Issue, error) {
	panic("unexpected")
}

func (*identityForge) RepliesAfter(context.Context, string, string, int, int64, string) ([]forge.Reply, error) {
	panic("unexpected")
}

func (*identityForge) PRState(context.Context, string, string, int) (string, error) {
	panic("unexpected")
}

func (*identityForge) CreateIssue(context.Context, string, string, string, string, []string) (int, error) {
	panic("unexpected")
}

func (*identityForge) React(context.Context, string, string, int, string) error { panic("unexpected") }

func (*identityForge) VerifyPush(context.Context, string, string) error { panic("unexpected") }

// A container-mode task owned by a non-root identity must have its RPC
// calls served by that identity's own forge client, never the root's.
// Before identity-scoped subjects existed, the single server set was wired
// to the root d.Forge/d.Trees, so one identity's agent could act through
// another identity's credential.
func TestRegisterTaskRPCServersRoutesIdentityScopedCalls(t *testing.T) {
	srv := startEmbeddedNATS(t)
	serverConn, err := natsio.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(serverConn.Close)

	st := store.OpenTest(t)
	rootForge := &identityForge{}
	archieForge := &identityForge{}
	rootTrees := &worktree.Manager{WorkDir: t.TempDir()}
	archieTrees := &worktree.Manager{WorkDir: t.TempDir()}

	identities := []*daemon.IdentityRunner{
		{Name: "archie", Forge: archieForge, Trees: archieTrees},
	}
	unsubscribe, err := registerTaskRPCServers(serverConn, st, rootForge, rootTrees, identities, worktreerpc.NewGrants(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("registerTaskRPCServers: %v", err)
	}
	t.Cleanup(unsubscribe)

	clientConn, err := natsio.Connect(srv.ClientURL())
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(clientConn.Close)

	ctx := context.Background()

	// An identity-scoped client must reach archie's forge, not the root's.
	archieClient := &forgerpc.Client{Conn: clientConn, Timeout: 2 * time.Second, Identity: "archie"}
	if err := archieClient.CloseIssue(ctx, "acme", "shared", 1, "done"); err != nil {
		t.Fatalf("identity-scoped CloseIssue: %v", err)
	}
	if archieForge.closes != 1 {
		t.Fatalf("identity forge closes = %d, want 1", archieForge.closes)
	}
	if rootForge.closes != 0 {
		t.Fatalf("root forge closes = %d, want 0 (identity call leaked to root)", rootForge.closes)
	}

	// The root (identity-less) client must reach the root forge.
	rootClient := &forgerpc.Client{Conn: clientConn, Timeout: 2 * time.Second}
	if err := rootClient.CloseIssue(ctx, "acme", "shared", 2, "done"); err != nil {
		t.Fatalf("root-scoped CloseIssue: %v", err)
	}
	if rootForge.closes != 1 {
		t.Fatalf("root forge closes = %d, want 1", rootForge.closes)
	}
}

func TestConfiguredNATSToken(t *testing.T) {
	t.Run("anonymous when token env is not configured", func(t *testing.T) {
		token, err := configuredNATSToken(config.NATSConfig{}, func(string) string {
			return ""
		})
		if err != nil || token != "" {
			t.Fatalf("configuredNATSToken() = (%q, %v), want empty token and nil error", token, err)
		}
	})

	t.Run("reads configured environment variable", func(t *testing.T) {
		token, err := configuredNATSToken(
			config.NATSConfig{TokenEnv: "ARCHIE_NATS_SECRET"},
			func(name string) string {
				if name == "ARCHIE_NATS_SECRET" {
					return "test-nats-token"
				}
				return ""
			},
		)
		if err != nil || token != "test-nats-token" {
			t.Fatalf("configuredNATSToken() = (%q, %v)", token, err)
		}
	})

	t.Run("rejects missing configured credential", func(t *testing.T) {
		_, err := configuredNATSToken(
			config.NATSConfig{TokenEnv: "ARCHIE_NATS_SECRET"},
			func(string) string { return "" },
		)
		if err == nil || !strings.Contains(err.Error(), "ARCHIE_NATS_SECRET") {
			t.Fatalf("configuredNATSToken() error = %v, want missing variable name", err)
		}
	})
}

// TestSubscribeAgentEventsPublishesDecodedEventsOnBus is the daemon-side half
// of the archie-core-518 fix: an archie-agent worker's ForwardTaskEvents
// ships events to SubjectForEvents(taskID); subscribeAgentEvents is what
// decodes them back and republishes on the daemon's own Bus, the single
// choke point persistAndBroadcastEvents drains into the SQLite events table
// and SSE feed. Without this half, ForwardTaskEvents ships into the void.
func TestSubscribeAgentEventsPublishesDecodedEventsOnBus(t *testing.T) {
	srv := startEmbeddedNATS(t)
	url := srv.ClientURL()

	serverConn, err := natsio.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(serverConn.Close)

	bus := events.NewBus()
	t.Cleanup(bus.Close)
	sub := bus.Subscribe(8)

	unsubscribe, err := subscribeAgentEvents(serverConn, bus, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("subscribeAgentEvents: %v", err)
	}
	t.Cleanup(unsubscribe)

	clientConn, err := natsio.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(clientConn.Close)

	want := events.Event{Kind: events.KindStageFinish, TaskID: 99, Workflow: "bootstrap", Stage: "apply"}
	payload, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientConn.Publish(agentexec.SubjectForEvents(99), payload); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := clientConn.Flush(); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-sub.C:
		if got.Kind != want.Kind || got.TaskID != want.TaskID || got.Workflow != want.Workflow || got.Stage != want.Stage {
			t.Errorf("bus received %+v, want %+v", got, want)
		}
		if got.ID != 0 {
			t.Errorf("bus event has ID %d set before store insertion, want 0 so persistAndBroadcastEvents inserts it fresh", got.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event arrived on the daemon bus within 2s")
	}
}

// TestSubscribeAgentEventsDropsUnparseableMessages proves the daemon does not
// panic or misroute when a message on the events wildcard subject doesn't
// match the shape SubjectForEvents/ForwardTaskEvents produce -- it is simply
// dropped, since nothing else is subscribed to this daemon-internal subject.
func TestSubscribeAgentEventsDropsUnparseableMessages(t *testing.T) {
	tests := []struct {
		name    string
		subject string
		payload []byte
	}{
		{"unparseable subject", "archie.agent.not-a-number.events", []byte(`{}`)},
		{"undecodable payload", agentexec.SubjectForEvents(1), []byte(`not json`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := startEmbeddedNATS(t)
			url := srv.ClientURL()

			serverConn, err := natsio.Connect(url)
			if err != nil {
				t.Fatalf("nats connect: %v", err)
			}
			t.Cleanup(serverConn.Close)

			bus := events.NewBus()
			t.Cleanup(bus.Close)
			sub := bus.Subscribe(8)

			unsubscribe, err := subscribeAgentEvents(serverConn, bus, slog.New(slog.DiscardHandler))
			if err != nil {
				t.Fatalf("subscribeAgentEvents: %v", err)
			}
			t.Cleanup(unsubscribe)

			clientConn, err := natsio.Connect(url)
			if err != nil {
				t.Fatalf("nats connect: %v", err)
			}
			t.Cleanup(clientConn.Close)

			if err := clientConn.Publish(tt.subject, tt.payload); err != nil {
				t.Fatalf("publish: %v", err)
			}
			// A well-formed control message on the same subject family proves
			// the subscriber is still alive and processing after the bad one,
			// rather than merely proving nothing arrived within the timeout.
			control := events.Event{Kind: events.KindStageStart, TaskID: 1}
			controlPayload, err := json.Marshal(control)
			if err != nil {
				t.Fatal(err)
			}
			if err := clientConn.Publish(agentexec.SubjectForEvents(1), controlPayload); err != nil {
				t.Fatalf("publish control: %v", err)
			}
			if err := clientConn.Flush(); err != nil {
				t.Fatal(err)
			}

			select {
			case got := <-sub.C:
				if got.Kind != control.Kind || got.TaskID != control.TaskID {
					t.Errorf("bus received %+v, want the control event %+v", got, control)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("control event never arrived; the bad message may have wedged the subscriber")
			}
		})
	}
}

func TestSubscribeAgentEventsFlushesBeforeReturning(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "flush task event subscription") {
		t.Fatal("subscribeAgentEvents does not flush its NATS subscription before returning")
	}
}
