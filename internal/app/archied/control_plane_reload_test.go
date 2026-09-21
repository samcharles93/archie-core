package archied

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
	"github.com/samcharles93/archie-core/internal/domain/workflow"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/workflowsteps"
)

// reloadSteps builds the production workflow step vocabulary the reload tests'
// control-plane client resolves definitions against.
func reloadSteps(t *testing.T) *workflow.Manager {
	t.Helper()
	steps, err := workflowsteps.NewManager()
	if err != nil {
		t.Fatalf("build workflow step vocabulary: %v", err)
	}
	return steps
}

// controlPlaneStub answers Query from a fixed resource map. queryErr, when
// set, fails every Query so a test can drive the unreachable-State-Store
// path.
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
	value, err := json.Marshal(c.values[request.Kind])
	return &pb.QueryResponse{Resource: &pb.Resource{Kind: request.Kind, Version: 3, ValueJson: value}}, err
}

func (*controlPlaneStub) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("unexpected History")
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
// SIGHUP reload re-resolves, holding none of the database's values.
func fileConfig() config.Config {
	return config.Config{
		BotUser:      "widget",
		Forge:        config.Forge{Type: "github"},
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
		controlPlane: controlplane.NewRPCClient(stub, reloadSteps(t)),
	}
	b.cfgHolder = config.NewHolder(b.cfg)
	return b
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
	b.applyWorkflowExecutionSettings(t.Context(), workflow.ExecutionSettings{
		MaxModelToolSteps: 99, MaxRuntime: time.Hour, MaxConsecutiveGateFailures: 5,
	}, 4)

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
