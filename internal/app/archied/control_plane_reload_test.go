package archied

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/secret"
)

// controlPlaneStub answers Query from a fixed resource map. queryErr, when
// set, fails every Query so a test can drive the unreachable-State-Store
// path. A kind the map does not carry is answered the way the server answers
// one with no stored value -- codes.NotFound (controlplane.mapError) -- so a
// test can drive the absent-resource state a skipped seed leaves behind.
type controlPlaneStub struct {
	values   map[string]any
	queryErr error
}

func (*controlPlaneStub) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	return &pb.CatalogResponse{}, nil
}

func (c *controlPlaneStub) Query(_ context.Context, request *pb.QueryRequest, _ ...grpc.CallOption) (*pb.QueryResponse, error) {
	if c.queryErr != nil {
		return nil, c.queryErr
	}
	value, ok := c.values[request.Kind]
	if !ok {
		return nil, status.Error(codes.NotFound, "resource not found")
	}
	encoded, err := json.Marshal(value)
	return &pb.QueryResponse{Resource: &pb.Resource{Kind: request.Kind, Version: 3, ValueJson: encoded}}, err
}

func (*controlPlaneStub) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("unexpected History")
}

func (*controlPlaneStub) Audit(context.Context, *pb.AuditRequest, ...grpc.CallOption) (*pb.AuditResponse, error) {
	return &pb.AuditResponse{}, nil
}

func (*controlPlaneStub) Command(context.Context, *pb.CommandRequest, ...grpc.CallOption) (*pb.CommandResponse, error) {
	panic("unexpected Command")
}

func (*controlPlaneStub) Watch(context.Context, *pb.WatchRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	panic("unexpected Watch")
}

// databaseOwnedResources is a control-plane projection whose every value
// differs from fileConfig below, so any field that comes back with the file
// value after a reload is a field the reload reverted.
func databaseOwnedResources() map[string]any {
	return map[string]any{
		controlplane.ProviderSettingsKind:         map[string]any{"main": map[string]any{"class": "openai", "base_url": "https://db.example.com"}},
		controlplane.ModelRoleAssignmentsKind:     map[string]string{"builder": "main/db-model"},
		controlplane.RepositoryPoliciesKind:       []config.Repo{{Owner: "acme", Name: "from-database"}},
		controlplane.ChannelSettingsKind:          map[string]any{"operator": "database", "max_steps": 20, "models": []string{"main/db-model"}, "email": map[string]any{}, "webhook": map[string]any{}, "telegram": map[string]any{"allowed_user_ids": []int64{42}}, "rate_limit": map[string]any{}, "workspace": "/workspace"},
		controlplane.SchedulingPolicyKind:         map[string]any{"poll_interval": "2m", "max_retries": 7, "dispatch": map[string]any{"trigger": "assignee"}},
		controlplane.ToolSettingsKind:             map[string]any{"mcp_servers": []map[string]any{}, "policy": map[string]any{}, "web_fetch": map[string]any{}, "minimax": map[string]any{}},
		controlplane.PluginSettingsKind:           map[string]any{"plugin_dir": "/db/plugins", "module_dir": "/db/modules", "secret_engine_dir": "/db/secrets", "skills_dir": "/db/skills"},
		controlplane.ContainerRuntimePoliciesKind: config.ContainerConfig{Image: "archie:from-database", PullPolicy: "missing"},
	}
}

// fileConfig is what the loader produces from config.toml alone: the layer a
// SIGHUP reload re-resolves, holding none of the database's values. It is a
// config the daemon would run, so it passes configuration.Validate -- the live
// apply path runs that same check over the snapshot it is about to publish --
// and every field validation reads is filled in the way the loader's defaults
// fill it, so a kind with no stored value can fall back to this document (see
// TestRuntimeConfigKeepsTheFileValueWhenAKindHasNoStoredResource).
func fileConfig() config.Config {
	return config.Config{
		BotUser:      "widget",
		Forge:        config.Forge{Type: "github"},
		Dispatch:     config.Dispatch{Trigger: "assignee"},
		Providers:    map[string]config.Provider{"file": {Class: "openai"}},
		Models:       map[string]string{"builder": "file/model"},
		Repos:        []config.Repo{{Owner: "acme", Name: "from-file"}},
		Chat:         config.ChatConfig{Operator: "file"},
		PollInterval: config.Duration(time.Minute),
		MaxRetries:   1,
		PluginDir:    "/file/plugins",
		Containers:   config.ContainerConfig{Image: "archie:from-file", PullPolicy: "missing"},
		Budgets:      config.Budgets{MaxSteps: 1, WallClock: config.Duration(time.Minute)},
	}
}

func newReloadBoot(t *testing.T, stub *controlPlaneStub) *boot {
	t.Helper()
	b := &boot{
		cfg:          fileConfig(),
		log:          slog.New(slog.DiscardHandler),
		controlPlane: controlplane.NewRPCClient(stub),
	}
	b.cfgHolder = config.NewHolder(b.cfg)
	return b
}

func TestRuntimeConfigResolvesStoredProviderReference(t *testing.T) {
	t.Setenv("ARCHIE_TEST_STORED_PROVIDER", "provider-secret")
	resources := databaseOwnedResources()
	resources[controlplane.ProviderSettingsKind] = map[string]any{"main": map[string]any{
		"class": "openai", "api_key_ref": map[string]any{"engine": "env", "key": "ARCHIE_TEST_STORED_PROVIDER"},
	}}
	b := newReloadBoot(t, &controlPlaneStub{values: resources})
	b.secrets = secret.NewRegistry()
	if err := b.loadRuntimeConfig(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := b.cfgHolder.Get().Providers["main"]
	if got.APIKey != (secret.SecretRef{}) || got.APIKeyEnv != providerSecretEnvName("root", "main") {
		t.Fatalf("effective provider credential = %+v, want a resolved process-local env name", got)
	}
	t.Cleanup(func() { _ = os.Unsetenv(got.APIKeyEnv) })
}

// TestReloadConfigKeepsDatabaseOwnedSettings is archie-core-ju85. The SIGHUP
// callback re-resolves the file document alone; without re-applying the
// control plane over it, every database-owned setting reverts to its file
// value until the process restarts, and the reload still logs success.
func TestReloadConfigKeepsDatabaseOwnedSettings(t *testing.T) {
	b := newReloadBoot(t, &controlPlaneStub{values: databaseOwnedResources()})
	if err := b.loadRuntimeConfig(t.Context()); err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}
	if err := b.applyWorkflowExecutionSettings(t.Context(), workflow.ExecutionSettings{
		MaxModelToolSteps: 99, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 5,
	}, 4); err != nil {
		t.Fatalf("applyWorkflowExecutionSettings: %v", err)
	}

	if err := b.reloadConfig(t.Context(), &configuration.Document{Config: fileConfig()}); err != nil {
		t.Fatalf("reloadConfig: %v", err)
	}

	got := b.cfgHolder.Get()
	for _, tt := range []struct {
		field string
		got   any
		want  any
	}{
		{"Providers", got.Providers["main"].BaseURL, "https://db.example.com"},
		{"Models", got.Models["builder"], "main/db-model"},
		{"Repos", got.Repos[0].Name, "from-database"},
		{"Chat", got.Chat.Operator, "database"},
		{"PollInterval", time.Duration(got.PollInterval), 2 * time.Minute},
		{"MaxRetries", got.MaxRetries, 7},
		{"PluginDir", got.PluginDir, "/db/plugins"},
		{"Containers", got.Containers.Image, "archie:from-database"},
		{"Budgets", got.Budgets.MaxSteps, 99},
	} {
		if tt.got != tt.want {
			t.Errorf("%s reverted to its file value after reload: got %v, want %v", tt.field, tt.got, tt.want)
		}
	}
	if got.BotUser != "widget" {
		t.Errorf("file-owned BotUser = %q, want the reloaded file value", got.BotUser)
	}
}

// TestReloadConfigFailsWhenControlPlaneUnreachable pins the direction of the
// failure: the running config already holds the database's values, so a
// control plane that cannot answer must abort the reload rather than publish
// the file's values over them.
func TestReloadConfigFailsWhenControlPlaneUnreachable(t *testing.T) {
	stub := &controlPlaneStub{values: databaseOwnedResources()}
	b := newReloadBoot(t, stub)
	if err := b.loadRuntimeConfig(t.Context()); err != nil {
		t.Fatalf("loadRuntimeConfig: %v", err)
	}

	stub.queryErr = context.DeadlineExceeded
	err := b.reloadConfig(t.Context(), &configuration.Document{Config: fileConfig()})
	if err == nil {
		t.Fatal("reloadConfig with an unreachable control plane: expected an error")
	}
	if got := b.cfgHolder.Get(); got.Models["builder"] != "main/db-model" {
		t.Fatalf("running config was replaced on the error path: Models = %v", got.Models)
	}
}
