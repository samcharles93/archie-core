package controlplane

import (
	"context"
	"encoding/json"
	"testing"

	"google.golang.org/grpc"

	"github.com/samcharles93/archie-core/internal/config"
	pb "github.com/samcharles93/archie-core/internal/contracts/controlplane/v1"
)

type runtimeConfigClient struct{ values map[string]any }

func (*runtimeConfigClient) Catalog(context.Context, *pb.CatalogRequest, ...grpc.CallOption) (*pb.CatalogResponse, error) {
	return &pb.CatalogResponse{}, nil
}

func (c *runtimeConfigClient) Query(_ context.Context, request *pb.QueryRequest, _ ...grpc.CallOption) (*pb.QueryResponse, error) {
	value, err := json.Marshal(c.values[request.Kind])
	return &pb.QueryResponse{Resource: &pb.Resource{Kind: request.Kind, Version: 2, ValueJson: value}}, err
}

func (*runtimeConfigClient) History(context.Context, *pb.HistoryRequest, ...grpc.CallOption) (*pb.HistoryResponse, error) {
	panic("unexpected History")
}

func (*runtimeConfigClient) Command(context.Context, *pb.CommandRequest, ...grpc.CallOption) (*pb.CommandResponse, error) {
	panic("unexpected Command")
}

func (*runtimeConfigClient) Watch(context.Context, *pb.WatchRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchResponse], error) {
	panic("unexpected Watch")
}

func TestRuntimeConfigUsesDatabaseResourcesAndPreservesBootstrapOnlySecrets(t *testing.T) {
	base := config.Config{
		Providers: map[string]config.Provider{"old": {Class: "openai"}},
		Models:    map[string]string{"builder": "old/model"},
		Repos:     []config.Repo{{Owner: "old", Name: "repo"}},
		Chat: config.ChatConfig{Telegram: config.TelegramConfig{
			UpdateCheckCommand: []string{"check"}, UpdateInstallCommand: []string{"install"},
		}},
		Tools: config.ToolsConfig{MCPServers: []config.MCPServer{{Name: "docs", Headers: map[string]string{"Authorization": "secret"}}}},
	}
	client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
		ProviderSettingsKind:         map[string]any{"main": map[string]any{"class": "openai", "base_url": "https://api.example.com", "api_key_ref": map[string]any{"engine": "env", "key": "API_KEY"}, "has_credential": true}},
		ModelRoleAssignmentsKind:     map[string]string{"builder": "main/model"},
		RepositoryPoliciesKind:       []config.Repo{{Owner: "acme", Name: "widget"}},
		ChannelSettingsKind:          map[string]any{"operator": "Sam", "show_tool_calls": true, "max_steps": 20, "models": []string{"main/model"}, "email": map[string]any{}, "webhook_addr": "", "webhook": map[string]any{}, "telegram": map[string]any{"allowed_user_ids": []int64{42}, "credential_configured": false}, "rate_limit": map[string]any{}, "unrestricted_filesystem": false, "workspace": "/workspace"},
		SchedulingPolicyKind:         map[string]any{"poll_interval": "2m", "max_retries": 7, "dispatch": map[string]any{"trigger": "assignee"}},
		ToolSettingsKind:             map[string]any{"mcp_servers": []map[string]any{{"name": "docs", "transport": "http", "url": "https://mcp.example.com", "headers_configured": true}}, "policy": map[string]any{}, "web_fetch": map[string]any{}, "minimax": map[string]any{"enabled": false, "credential_configured": false}},
		PluginSettingsKind:           map[string]any{"plugin_dir": "/plugins", "module_dir": "/modules", "secret_engine_dir": "/secrets", "skills_dir": "/skills"},
		ContainerRuntimePoliciesKind: config.ContainerConfig{LegacyEnabled: true, Image: "archie:next", PullPolicy: "missing"},
	}})

	got, err := client.RuntimeConfig(t.Context(), base)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if got.Models["builder"] != "main/model" || got.Repos[0].FullName() != "acme/widget" || got.MaxRetries != 7 || got.PluginDir != "/plugins" || got.Containers.Image != "archie:next" {
		t.Fatalf("database settings not applied: %+v", got)
	}
	if got.Chat.Operator != "Sam" || got.Chat.Telegram.AllowedUserIDs[0] != 42 {
		t.Fatalf("channel settings not applied: %+v", got.Chat)
	}
	if got.Chat.Telegram.UpdateCheckCommand[0] != "check" || got.Tools.MCPServers[0].Headers["Authorization"] != "secret" {
		t.Fatalf("bootstrap-only secret-backed settings were dropped: chat=%+v tools=%+v", got.Chat, got.Tools)
	}
}
