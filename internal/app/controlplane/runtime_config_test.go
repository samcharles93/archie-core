package controlplane

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
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

func (*runtimeConfigClient) Audit(context.Context, *pb.AuditRequest, ...grpc.CallOption) (*pb.AuditResponse, error) {
	return &pb.AuditResponse{}, nil
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
		ContainerRuntimePoliciesKind: map[string]any{"image": "archie:next", "pull_policy": "missing"},
		AgentProfileKind:             map[string]any{"net": map[string]any{"tools": []string{"whois"}}},
		CredentialBindingsKind:       []map[string]any{{"service": "openai", "org": "acme", "secret": map[string]any{"engine": "env", "key": "OPENAI_KEY"}}},
	}})

	got, versions, err := client.RuntimeConfig(t.Context(), base)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	// Every kind it read is reported as applied, so the version a process
	// publishes is the one it actually layered in (archie-core-pskb).
	for _, kind := range []string{
		ProviderSettingsKind, ModelRoleAssignmentsKind, RepositoryPoliciesKind, ChannelSettingsKind,
		SchedulingPolicyKind, ToolSettingsKind, PluginSettingsKind, ContainerRuntimePoliciesKind, AgentProfileKind, CredentialBindingsKind,
	} {
		if versions[kind] != 2 {
			t.Errorf("versions[%s] = %d, want the 2 the store answered with", kind, versions[kind])
		}
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

// TestRuntimeConfigReplacesStoredModelRolesEntirely: the stored role
// assignments own cfg.Models once the store carries a value, the way the
// provider settings own cfg.Providers, so a role removed from the store is
// gone from the layered document too. The runtime-resource watches re-layer
// over an already-layered base (archie-core-zfb0.1), so a merge here would
// resurrect a role the store deleted every time any watched kind changed.
func TestRuntimeConfigReplacesStoredModelRolesEntirely(t *testing.T) {
	base := config.Config{Models: map[string]string{"builder": "old/model", "stale": "old/stale"}}
	client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
		ModelRoleAssignmentsKind: map[string]string{"builder": "main/model"},
		SchedulingPolicyKind:     map[string]any{"poll_interval": "2m", "max_retries": 7, "dispatch": map[string]any{"trigger": "assignee"}},
	}})

	got, _, err := client.RuntimeConfig(t.Context(), base)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if !reflect.DeepEqual(got.Models, map[string]string{"builder": "main/model"}) {
		t.Fatalf("layered roles = %v, want the stored document alone", got.Models)
	}
}

func TestRuntimeConfigRejectsStoredBootDerivedProviderName(t *testing.T) {
	stored := []byte(`{"openai":{"class":"openai","api_key_env":"ARCHIE_PROVIDER_6986D5A05E1DF674_API_KEY"}}`)
	if err := validateProviders(stored); err == nil {
		t.Fatal("replace accepted a boot-derived provider name")
	}
	_, _, err := runtimeConfigFrom(t.Context(), providerSettingsReader{value: stored}, config.Config{})
	if err == nil || !strings.Contains(err.Error(), "ARCHIE_PROVIDER_6986D5A05E1DF674_API_KEY") || !strings.Contains(err.Error(), "replace provider-settings") {
		t.Fatalf("RuntimeConfig error = %v, want the stored derived name and repair action", err)
	}
}

type providerSettingsReader struct{ value []byte }

func (r providerSettingsReader) Query(_ context.Context, kind string, decode func([]byte) error) (int64, bool, error) {
	if kind != ProviderSettingsKind {
		return 0, false, nil
	}
	return 2, true, decode(r.value)
}

// TestRuntimeConfigLayersTheStoredSchedulingLabel: the label pairs with the
// dispatch trigger and layers with it. A policy that carries one replaces the
// file document's label, so the store owns the half of the pairing it judged
// at write time (archie-core-7pyj).
func TestRuntimeConfigLayersTheStoredSchedulingLabel(t *testing.T) {
	base := config.Config{
		Label:        "file-label",
		PollInterval: config.Duration(60 * 1e9),
		Dispatch:     config.Dispatch{Trigger: "assignee"},
	}
	client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
		SchedulingPolicyKind: map[string]any{"poll_interval": "2m", "max_retries": 7, "label": "stored-label", "dispatch": map[string]any{"trigger": "assignee"}},
	}})
	got, _, err := client.RuntimeConfig(t.Context(), base)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if got.Label != "stored-label" {
		t.Fatalf("label after layering = %q, want the stored policy's label", got.Label)
	}
}

// TestRuntimeConfigLeavesTheFileLabelInForceWhenThePolicyCarriesNone: a
// policy stored before the label field existed (or one that omits it) is not
// an instruction to clear the file document's label -- the same
// absence-means-inherited shape a kind with no stored value has.
func TestRuntimeConfigLeavesTheFileLabelInForceWhenThePolicyCarriesNone(t *testing.T) {
	base := config.Config{
		Label:        "file-label",
		PollInterval: config.Duration(60 * 1e9),
		Dispatch:     config.Dispatch{Trigger: "assignee"},
	}
	client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
		SchedulingPolicyKind: map[string]any{"poll_interval": "2m", "max_retries": 7, "dispatch": map[string]any{"trigger": "assignee"}},
	}})
	got, _, err := client.RuntimeConfig(t.Context(), base)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if got.Label != "file-label" {
		t.Fatalf("label after layering = %q, want the file document's label left in force", got.Label)
	}
}

// TestRuntimeConfigCarriesParallelToolCallsThroughTheProjection: the layering
// rebuilds cfg.Tools from the control plane's own projection wholesale
// (runtime_config.go), so a field the projection has no home for is dropped for
// every deployment that runs a control plane -- file-level
// parallel_tool_calls = true would be accepted, shown, and inert. The value has
// to survive both conversions: the seed that writes the resource and the
// layering that reads it back.
func TestRuntimeConfigCarriesParallelToolCallsThroughTheProjection(t *testing.T) {
	base := config.Config{Tools: config.ToolsConfig{MCPServers: []config.MCPServer{{
		Name: "docs", Transport: "stdio", Command: "docs-server", ParallelToolCalls: true,
	}}}}

	seed := seedToolSettings(t, base)
	if carried, _ := seededServer(t, seed)["parallel_tool_calls"].(bool); !carried {
		t.Errorf("%s seed = %s, want the file document's parallel_tool_calls in it", ToolSettingsKind, seed)
	}
	got := layerToolSettings(t, seed, base)
	if !got.Tools.MCPServers[0].ParallelToolCalls {
		t.Fatalf("ParallelToolCalls after the control-plane round trip = false, want true: %+v", got.Tools.MCPServers[0])
	}
}

// settingsProjectionSpec describes a source config struct and the explicit
// control-plane document struct that projects it. Overrides cover renamed or
// transformed keys; the two allowlists require a reason for one-way fields.
type settingsProjectionSpec struct {
	name           string
	source         any
	projection     any
	keyOverrides   map[string]string
	fileSurrogates map[string]string
	leftToFile     map[string]string
	projectionOnly map[string]string
}

// settingsProjectionSpecs includes the custom config mirrors in this package:
// provider and tool resources, plus the channel document types aliased from
// controlplanerpc. Repo and container resources carry config types directly.
var settingsProjectionSpecs = []settingsProjectionSpec{
	{
		name: "tools", source: config.ToolsConfig{}, projection: toolSettings{},
		keyOverrides: map[string]string{"Policy": "policy"},
	},
	{
		name: "MCP server", source: config.MCPServer{}, projection: mcpServerSettings{},
		fileSurrogates: map[string]string{"Headers": "headers_configured"},
		leftToFile:     map[string]string{"Headers": "HTTP credentials remain file-owned"},
	},
	{
		name: "MiniMax", source: config.MinimaxConfig{}, projection: minimaxSettings{},
		keyOverrides:   map[string]string{"APIKey": "api_key_ref"},
		projectionOnly: map[string]string{"CredentialConfigured": "derived from APIKey"},
	},
	{
		name: "provider", source: config.Provider{}, projection: providerDocument{},
		keyOverrides:   map[string]string{"APIKey": "api_key_ref"},
		projectionOnly: map[string]string{"HasCredential": "derived from APIKey and APIKeyEnv"},
	},
	{name: "chat", source: config.ChatConfig{}, projection: channelSettings{}},
	{
		name: "Telegram", source: config.TelegramConfig{}, projection: telegramSettings{},
		keyOverrides: map[string]string{"Token": "token_ref"},
		leftToFile: map[string]string{
			"UpdateCheckCommand":   "update commands stay file-owned",
			"UpdateInstallCommand": "update commands stay file-owned",
		},
		projectionOnly: map[string]string{"CredentialConfigured": "derived from Token"},
	},
	{
		name: "webhook route", source: config.WebhookRoute{}, projection: webhookChannelSettings{},
		keyOverrides:   map[string]string{"Secret": "secret_ref"},
		projectionOnly: map[string]string{"CredentialConfigured": "derived from Secret"},
	},
	{name: "email", source: config.EmailConfig{}, projection: emailSettings{}},
	{name: "rate limit", source: config.RateLimitConfig{}, projection: rateLimitSettings{}},
}

func TestSettingsProjectionTypesStayInParity(t *testing.T) {
	for _, spec := range settingsProjectionSpecs {
		t.Run(spec.name, func(t *testing.T) {
			sourceType, projectionType := reflect.TypeOf(spec.source), reflect.TypeOf(spec.projection)
			projectionKeys := make(map[string]string, projectionType.NumField())
			for field := range projectionType.Fields() {
				projectionKeys[jsonFieldName(field)] = field.Name
			}

			mappedKeys := map[string]string{}
			for field := range sourceType.Fields() {
				key := spec.keyOverrides[field.Name]
				if key == "" {
					key = configFieldName(field)
				}
				if reason, allowed := spec.leftToFile[field.Name]; allowed {
					if reason == "" {
						t.Errorf("%s.%s has an empty left-to-file reason", spec.name, field.Name)
					}
					if surrogate := spec.fileSurrogates[field.Name]; surrogate != "" {
						if _, exists := projectionKeys[surrogate]; !exists {
							t.Errorf("%s.%s is left to the file but projection has no surrogate key %q", spec.name, field.Name, surrogate)
						}
						mappedKeys[surrogate] = field.Name
					}
					continue
				}
				if key == "" {
					t.Errorf("%s.%s has no config or projection key; add an explicit key override or left-to-file reason", spec.name, field.Name)
					continue
				}
				if _, exists := projectionKeys[key]; !exists {
					t.Errorf("%s.%s maps to %q, which %s does not project", spec.name, field.Name, key, projectionType)
				}
				mappedKeys[key] = field.Name
			}

			for name, reason := range spec.leftToFile {
				if _, exists := sourceType.FieldByName(name); !exists {
					t.Errorf("%s left-to-file allowlist names missing source field %s", spec.name, name)
				}
				if reason == "" {
					t.Errorf("%s left-to-file allowlist for %s has no reason", spec.name, name)
				}
			}
			for key, fieldName := range spec.keyOverrides {
				if _, exists := sourceType.FieldByName(key); !exists {
					t.Errorf("%s key override names missing source field %s", spec.name, key)
				}
				if _, exists := projectionKeys[fieldName]; !exists {
					t.Errorf("%s key override names missing projection key %q", spec.name, fieldName)
				}
			}
			for fieldName, key := range spec.fileSurrogates {
				if _, exists := sourceType.FieldByName(fieldName); !exists {
					t.Errorf("%s file-surrogate mapping names missing source field %s", spec.name, fieldName)
				}
				if _, exists := projectionKeys[key]; !exists {
					t.Errorf("%s file-surrogate mapping names missing projection key %q", spec.name, key)
				}
				if _, allowed := spec.leftToFile[fieldName]; !allowed {
					t.Errorf("%s.%s has file surrogate %q but is not allowed to remain file-owned", spec.name, fieldName, key)
				}
			}
			for field := range projectionType.Fields() {
				key := jsonFieldName(field)
				if _, exists := mappedKeys[key]; exists {
					continue
				}
				if reason, allowed := spec.projectionOnly[field.Name]; !allowed || reason == "" {
					t.Errorf("%s.%s projects JSON key %q with no source field or derived-field reason", spec.name, field.Name, key)
				}
			}
			for name := range spec.projectionOnly {
				if _, exists := projectionType.FieldByName(name); !exists {
					t.Errorf("%s projection-only allowlist names missing projection field %s", spec.name, name)
				}
			}
		})
	}
}

func configFieldName(field reflect.StructField) string {
	for _, tag := range []string{"toml", "json"} {
		name, _, _ := strings.Cut(field.Tag.Get(tag), ",")
		if name == "-" {
			return ""
		}
		if name != "" {
			return name
		}
	}
	return field.Name
}

func TestToolSettingsProjectionCarriesEveryMirroredField(t *testing.T) {
	tests := []struct {
		name          string
		source        any
		configure     func(*config.Config, reflect.Value)
		projectedItem func(*testing.T, []byte) map[string]any
		result        func(config.Config) reflect.Value
	}{
		{
			name:   "MCP server",
			source: config.MCPServer{},
			configure: func(cfg *config.Config, value reflect.Value) {
				server, ok := value.Interface().(config.MCPServer)
				if !ok {
					t.Fatalf("source type = %T, want config.MCPServer", value.Interface())
				}
				cfg.Tools.MCPServers = []config.MCPServer{server}
			},
			projectedItem: seededServer,
			result:        func(cfg config.Config) reflect.Value { return reflect.ValueOf(cfg.Tools.MCPServers[0]) },
		},
		{
			name:   "MiniMax",
			source: config.MinimaxConfig{},
			configure: func(cfg *config.Config, value reflect.Value) {
				minimax, ok := value.Interface().(config.MinimaxConfig)
				if !ok {
					t.Fatalf("source type = %T, want config.MinimaxConfig", value.Interface())
				}
				cfg.Tools.Minimax = minimax
			},
			projectedItem: func(t *testing.T, seed []byte) map[string]any {
				t.Helper()
				var document struct {
					Minimax map[string]any `json:"minimax"`
				}
				if err := json.Unmarshal(seed, &document); err != nil {
					t.Fatalf("decode %s seed: %v", ToolSettingsKind, err)
				}
				return document.Minimax
			},
			result: func(cfg config.Config) reflect.Value { return reflect.ValueOf(cfg.Tools.Minimax) },
		},
	}

	specs := make(map[string]settingsProjectionSpec, len(settingsProjectionSpecs))
	for _, spec := range settingsProjectionSpecs {
		specs[spec.name] = spec
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := reflect.New(reflect.TypeOf(tt.source)).Elem()
			populateEveryField(t, source, "config."+source.Type().Name())
			if tt.name == "MCP server" {
				source.FieldByName("Transport").SetString("stdio") // the validator's supported value
			}
			base := config.Config{}
			tt.configure(&base, source)
			seed := seedToolSettings(t, base)
			seeded := tt.projectedItem(t, seed)
			got := tt.result(layerToolSettings(t, seed, base))
			spec := specs[tt.name]
			for i := range source.NumField() {
				field := source.Type().Field(i)
				key := spec.keyOverrides[field.Name]
				if key == "" {
					key = configFieldName(field)
				}
				surrogate := spec.fileSurrogates[field.Name]
				_, leftToFile := spec.leftToFile[field.Name]
				switch {
				case surrogate != "":
					if _, ok := seeded[surrogate]; !ok {
						t.Errorf("%s seed has no %q surrogate for file-owned %s.%s", ToolSettingsKind, surrogate, source.Type(), field.Name)
					}
				case leftToFile:
					if key != "" {
						if _, ok := seeded[key]; ok {
							t.Errorf("%s seed unexpectedly carries file-owned %s.%s as %q", ToolSettingsKind, source.Type(), field.Name, key)
						}
					}
				default:
					if _, ok := seeded[key]; !ok {
						t.Errorf("%s seed has no %q key for %s.%s", ToolSettingsKind, key, source.Type(), field.Name)
					}
				}
				if gotField, wantField := got.Field(i).Interface(), source.Field(i).Interface(); !reflect.DeepEqual(gotField, wantField) {
					t.Errorf("%s.%s after round trip = %#v, want %#v", source.Type(), field.Name, gotField, wantField)
				}
			}
		})
	}
}

// seedToolSettings is the value a State Store writes for the tool settings when
// it holds none for that kind: the production seed derived from the file
// document, run through the production Decode exactly as ImportConfig writes it.
func seedToolSettings(t *testing.T, base config.Config) []byte {
	t.Helper()
	seed, err := testServer(t, nil).definitions[ToolSettingsKind].seededValue(base)
	if err != nil {
		t.Fatalf("seed %s: %v", ToolSettingsKind, err)
	}
	return seed
}

// layerToolSettings layers a stored tool-settings value over base with the
// production layering, answering every other kind as absent -- the shape of a
// store that has seeded nothing else, where the file document's value stays in
// force.
func layerToolSettings(t *testing.T, stored []byte, base config.Config) config.Config {
	t.Helper()
	got, _, err := runtimeConfigFrom(t.Context(), toolSettingsReader{value: stored}, base)
	if err != nil {
		t.Fatalf("layer %s: %v", ToolSettingsKind, err)
	}
	return got
}

// toolSettingsReader answers the one kind under test and reports every other
// kind absent.
type toolSettingsReader struct{ value []byte }

func (r toolSettingsReader) Query(_ context.Context, kind string, decode func([]byte) error) (int64, bool, error) {
	if kind != ToolSettingsKind {
		return 0, false, nil
	}
	return 2, true, decode(r.value)
}

// seededServer is the MCP server entry the seed actually wrote for the one
// server in the config: the projection's own output, which is what the layering
// reads back.
func seededServer(t *testing.T, seed []byte) map[string]any {
	t.Helper()
	var document struct {
		MCPServers []map[string]any `json:"mcp_servers"`
	}
	if err := json.Unmarshal(seed, &document); err != nil {
		t.Fatalf("decode %s seed: %v", ToolSettingsKind, err)
	}
	if len(document.MCPServers) != 1 {
		t.Fatalf("%s seed carries %d MCP servers, want the one the config has: %s", ToolSettingsKind, len(document.MCPServers), seed)
	}
	return document.MCPServers[0]
}

// jsonFieldName is the key a field's value travels under in a stored resource.
func jsonFieldName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	return name
}

// populateEveryField sets every field of value to a distinct non-zero value
// derived from its type, so a struct compared before and after a round trip
// differs on every field that round trip drops. Nothing about the struct is
// written down here: a field added to config.MCPServer later is populated
// without an edit, which is what lets the projection guard fail for a field
// nobody remembered to add to a fixture.
func populateEveryField(t *testing.T, value reflect.Value, label string) {
	t.Helper()
	if !value.CanSet() {
		t.Fatalf("%s cannot be set, so the round-trip fixture cannot populate it", label)
	}
	switch value.Kind() {
	case reflect.String:
		value.SetString("value-for-" + label)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Slice:
		value.Set(reflect.MakeSlice(value.Type(), 1, 1))
		populateEveryField(t, value.Index(0), label)
	case reflect.Map:
		entry := reflect.MakeMap(value.Type())
		key, item := reflect.New(value.Type().Key()).Elem(), reflect.New(value.Type().Elem()).Elem()
		populateEveryField(t, key, label)
		populateEveryField(t, item, label)
		entry.SetMapIndex(key, item)
		value.Set(entry)
	case reflect.Struct:
		for i := range value.NumField() {
			populateEveryField(t, value.Field(i), label+"."+value.Type().Field(i).Name)
		}
	default:
		t.Fatalf("%s has kind %s, which the round-trip fixture cannot populate: teach populateEveryField that kind, or the projection guard cannot see the field", label, value.Kind())
	}
}

// TestSchedulingPolicySeedCarriesTheLabel: the seed a fresh store writes must
// carry the file document's label, so the pairing a stored trigger requires
// is judgeable at write time from the moment the resource exists.
// TestRuntimeResourceKindsApplyLive pins the four kinds the daemon re-layers
// live (archie-core-zfb0.1) and the ones a startup-built consumer still
// freezes, so a flip in either direction is a deliberate edit to the
// definition, not a default that drifts.
func TestRuntimeResourceKindsApplyLive(t *testing.T) {
	t.Parallel()

	registry, err := stepRegistry(testSteps(t))
	if err != nil {
		t.Fatalf("build step registry: %v", err)
	}
	modes := make(map[string]string)
	for _, definition := range builtinDefinitions(registry) {
		modes[definition.Kind] = definition.ApplyMode
	}
	for _, kind := range []string{
		ProviderSettingsKind, ModelRoleAssignmentsKind, RepositoryPoliciesKind, SchedulingPolicyKind, AgentProfileKind, CredentialBindingsKind,
	} {
		if modes[kind] != "live" {
			t.Errorf("%s applies %q, want live: the daemon re-layers this kind on a watch", kind, modes[kind])
		}
	}
	for _, kind := range []string{ToolSettingsKind, PluginSettingsKind, ContainerRuntimePoliciesKind, ChannelSettingsKind} {
		if modes[kind] != "restart-required" {
			t.Errorf("%s applies %q, want restart-required: a startup-built component still holds it", kind, modes[kind])
		}
	}
}

func TestSchedulingPolicySeedCarriesTheLabel(t *testing.T) {
	server := testServer(t, nil)
	seed, err := server.definitions[SchedulingPolicyKind].seededValue(config.Config{
		Label:        "archie:labelled",
		PollInterval: config.Duration(60 * 1e9),
		Dispatch:     config.Dispatch{Trigger: "label"},
	})
	if err != nil {
		t.Fatalf("seededValue: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(seed, &document); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	if label, _ := document["label"].(string); label != "archie:labelled" {
		t.Fatalf("seed label = %q, want the file document's label", label)
	}
}

// absentReader answers every kind as not-found, so a test can exercise
// runtimeConfigFrom's leave-the-file-value-in-effect path (real ImportConfig
// never leaves a kind unseeded, but a store the migration has not reached yet
// -- or a resource kind added after this document was last stored -- does).
type absentReader struct{}

func (absentReader) Query(context.Context, string, func([]byte) error) (int64, bool, error) {
	return 0, false, nil
}

// A stored AgentProfileKind value replaces the file's profiles outright; no
// stored value at all leaves the file's in effect (agent-profiles is its own
// resource, seeded from but independent of container-runtime-policies, so a
// Kit profile applies without a restart -- docs/prds/external-agent-harness.md
// "Selection").
func TestRuntimeConfigLayersStoredProfiles(t *testing.T) {
	base := config.Config{Containers: config.ContainerConfig{Image: "agent:1", Profiles: map[string]config.AgentProfile{
		"file-only": {Image: "file:1"},
	}}}

	for _, tt := range []struct {
		name   string
		stored map[string]any
		want   []string
	}{
		{name: "a stored value replaces the file's", stored: map[string]any{"net": map[string]any{"tools": []string{"whois"}}}, want: []string{"net"}},
		{
			// The Go-cased spelling documents written before agent-profiles had
			// its own document shape carry, at the profile-nesting level
			// (agentProfile.UnmarshalJSON): the profile it names must still
			// replace the file's.
			name:   "a legacy Go-cased stored value replaces the file's",
			stored: map[string]any{"net": map[string]any{"Tools": []string{"whois"}}},
			want:   []string{"net"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
				SchedulingPolicyKind:         map[string]any{"poll_interval": "2m", "dispatch": map[string]any{"trigger": "assignee"}},
				ContainerRuntimePoliciesKind: map[string]any{"image": "agent:2"},
				AgentProfileKind:             tt.stored,
			}})
			got, _, err := client.RuntimeConfig(t.Context(), base)
			if err != nil {
				t.Fatalf("RuntimeConfig: %v", err)
			}
			var names []string
			for name := range got.Containers.Profiles {
				names = append(names, name)
			}
			if !reflect.DeepEqual(names, tt.want) {
				t.Fatalf("profiles = %v, want %v", names, tt.want)
			}
		})
	}

	t.Run("no stored value inherits the file's", func(t *testing.T) {
		got, _, err := runtimeConfigFrom(t.Context(), absentReader{}, base)
		if err != nil {
			t.Fatalf("runtimeConfigFrom: %v", err)
		}
		var names []string
		for name := range got.Containers.Profiles {
			names = append(names, name)
		}
		if want := []string{"file-only"}; !reflect.DeepEqual(names, want) {
			t.Fatalf("profiles = %v, want %v", names, want)
		}
	})

	if got := base.Containers.Profiles; len(got) != 1 {
		t.Fatalf("layering mutated the file document's profiles: %v", got)
	}
}

// A stored container-runtime-policies document replaces the file's [containers]
// section wholesale, so a file-owned field that document does not carry -- the
// registry credential resolved at boot -- must be preserved across the
// assignment or it is silently dropped and every private-registry pull is
// anonymous again.
func TestRuntimeConfigPreservesFileOwnedRegistryAuth(t *testing.T) {
	base := config.Config{Containers: config.ContainerConfig{
		Image:        "agent:1",
		RegistryAuth: config.SecretRef{Engine: "env", Key: "REGISTRY_AUTH"},
	}}

	client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
		SchedulingPolicyKind:         map[string]any{"poll_interval": "2m", "dispatch": map[string]any{"trigger": "assignee"}},
		ContainerRuntimePoliciesKind: map[string]any{"image": "agent:2", "pull_policy": "always"},
	}})
	got, _, err := client.RuntimeConfig(t.Context(), base)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if got.Containers.Image != "agent:2" {
		t.Fatalf("image = %q, want the stored document's", got.Containers.Image)
	}
	if want := (config.SecretRef{Engine: "env", Key: "REGISTRY_AUTH"}); got.Containers.RegistryAuth != want {
		t.Fatalf("registry_auth = %+v, want the file's %+v preserved across the container-policies layering", got.Containers.RegistryAuth, want)
	}
}

// A stored CredentialBindingsKind value replaces the file's bindings
// outright; no stored value at all leaves the file's in effect (the same
// shape TestRuntimeConfigLayersStoredProfiles pins for AgentProfileKind, for
// the same reason: a binding applies without a restart).
func TestRuntimeConfigLayersStoredCredentialBindings(t *testing.T) {
	base := config.Config{Containers: config.ContainerConfig{Image: "agent:1", Credentials: []config.CredentialBinding{
		{Service: "file-only", Org: "acme"},
	}}}

	t.Run("a stored value replaces the file's", func(t *testing.T) {
		client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
			SchedulingPolicyKind:         map[string]any{"poll_interval": "2m", "dispatch": map[string]any{"trigger": "assignee"}},
			ContainerRuntimePoliciesKind: map[string]any{"image": "agent:2"},
			CredentialBindingsKind:       []map[string]any{{"service": "openai", "org": "acme", "secret": map[string]any{"engine": "env", "key": "OPENAI_KEY"}}},
		}})
		got, _, err := client.RuntimeConfig(t.Context(), base)
		if err != nil {
			t.Fatalf("RuntimeConfig: %v", err)
		}
		if len(got.Containers.Credentials) != 1 || got.Containers.Credentials[0].Service != "openai" || got.Containers.Credentials[0].Secret.Key != "OPENAI_KEY" {
			t.Fatalf("credentials = %+v, want the stored binding", got.Containers.Credentials)
		}
	})

	t.Run("no stored value inherits the file's", func(t *testing.T) {
		got, _, err := runtimeConfigFrom(t.Context(), absentReader{}, base)
		if err != nil {
			t.Fatalf("runtimeConfigFrom: %v", err)
		}
		if len(got.Containers.Credentials) != 1 || got.Containers.Credentials[0].Service != "file-only" {
			t.Fatalf("credentials = %+v, want the file's binding", got.Containers.Credentials)
		}
	})

	if got := base.Containers.Credentials; len(got) != 1 {
		t.Fatalf("layering mutated the file document's credentials: %v", got)
	}
}
