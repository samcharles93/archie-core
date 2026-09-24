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
		ContainerRuntimePoliciesKind: config.ContainerConfig{Image: "archie:next", PullPolicy: "missing"},
	}})

	got, versions, err := client.RuntimeConfig(t.Context(), base)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	// Every kind it read is reported as applied, so the version a process
	// publishes is the one it actually layered in (archie-core-pskb).
	for _, kind := range []string{
		ProviderSettingsKind, ModelRoleAssignmentsKind, RepositoryPoliciesKind, ChannelSettingsKind,
		SchedulingPolicyKind, ToolSettingsKind, PluginSettingsKind, ContainerRuntimePoliciesKind,
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

// TestToolSettingsProjectionCarriesEveryMCPServerField is the guard the field
// itself cannot be. mcpServerSettings mirrors config.MCPServer field by field,
// so the next field added to MCPServer -- internal/config's Sandboxed, when
// mcp-tool-completion's #178 lands -- silently falls out of the control-plane
// path unless something fails when it does. This test is that something: it
// populates every MCPServer field by reflection, so a field added later is
// populated without editing any fixture here, then requires the seed to name
// the field and the layering to hand it back.
//
// mcpServerFieldsLeftToTheFile is the only way past the check, and every entry
// in it has to say what the projection carries in place of the field.
func TestToolSettingsProjectionCarriesEveryMCPServerField(t *testing.T) {
	var server config.MCPServer
	populateEveryField(t, reflect.ValueOf(&server).Elem(), "config.MCPServer")
	// The resource validator knows a fixed transport vocabulary, so the round
	// trip needs one of them. It is the only value here the fixture takes from
	// the validator rather than from the field's type; stdio requires a command,
	// which the fixture has already set.
	server.Transport = "stdio"
	base := config.Config{Tools: config.ToolsConfig{MCPServers: []config.MCPServer{server}}}

	seed := seedToolSettings(t, base)
	seeded := seededServer(t, seed)
	got := layerToolSettings(t, seed, base).Tools.MCPServers[0]

	typ := reflect.TypeOf(server)
	for i := range typ.NumField() {
		field := typ.Field(i)
		name := jsonFieldName(field)
		surrogate, leftToTheFile := mcpServerFieldsLeftToTheFile[field.Name]
		if leftToTheFile {
			// Deliberately not carried: the stored value must not hold the
			// field's own value, and whatever the projection carries instead
			// must be there.
			if _, carried := seeded[name]; carried {
				t.Errorf("the %s seed carries MCPServer.%s (%q), which mcpServerFieldsLeftToTheFile leaves to the file document", ToolSettingsKind, field.Name, name)
			}
			if _, ok := seeded[surrogate]; !ok {
				t.Errorf("the %s seed carries neither MCPServer.%s nor the %q the projection carries in its place", ToolSettingsKind, field.Name, surrogate)
			}
			continue
		}
		if _, carried := seeded[name]; !carried {
			t.Errorf("the %s seed has no %q key, so seedTools never projects MCPServer.%s and the layering can only restore its zero value", ToolSettingsKind, name, field.Name)
			continue
		}
		wantField := reflect.ValueOf(server).Field(i).Interface()
		if gotField := reflect.ValueOf(got).Field(i).Interface(); !reflect.DeepEqual(gotField, wantField) {
			t.Errorf("MCPServer.%s = %#v after the control-plane round trip, want %#v: carry it through mcpServerSettings, seedTools, and runtimeToolConfigFrom, or add it to mcpServerFieldsLeftToTheFile with what the projection carries in its place", field.Name, gotField, wantField)
		}
	}
	// An allowlist entry for a field MCPServer no longer has is a dead pass.
	for name := range mcpServerFieldsLeftToTheFile {
		if _, ok := typ.FieldByName(name); !ok {
			t.Errorf("mcpServerFieldsLeftToTheFile names MCPServer.%s, which the struct no longer has", name)
		}
	}
}

// mcpServerFieldsLeftToTheFile names the MCPServer fields the control plane's
// projection deliberately does not carry, with the projection key that stands
// in for each. It is deliberately short: a field belongs here only when the
// file document has to stay its owner. Headers hold credentials, so the stored
// resource records only that some are configured and the layering restores the
// real map from the base config by server name (runtime_config.go), which
// TestRuntimeConfigUsesDatabaseResourcesAndPreservesBootstrapOnlySecrets pins.
var mcpServerFieldsLeftToTheFile = map[string]string{
	"Headers": "headers_configured",
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

// Stored container policies that carry profiles replace the file's outright;
// ones stored before profiles existed inherit the file's.
func TestRuntimeConfigLayersStoredProfiles(t *testing.T) {
	base := config.Config{Containers: config.ContainerConfig{Image: "agent:1", Profiles: map[string]config.AgentProfile{
		"file-only": {Image: "file:1"},
	}}}
	for _, tt := range []struct {
		name   string
		stored map[string]any
		want   []string
	}{
		{name: "stored profiles replace the file's", stored: map[string]any{"Image": "agent:2", "Profiles": map[string]any{"net": map[string]any{"Tools": []string{"whois"}}}}, want: []string{"net"}},
		{name: "a document without profiles inherits the file's", stored: map[string]any{"Image": "agent:2"}, want: []string{"file-only"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := NewRPCClient(&runtimeConfigClient{values: map[string]any{
				SchedulingPolicyKind:         map[string]any{"poll_interval": "2m", "dispatch": map[string]any{"trigger": "assignee"}},
				ContainerRuntimePoliciesKind: tt.stored,
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
	if got := base.Containers.Profiles; len(got) != 1 {
		t.Fatalf("layering mutated the file document's profiles: %v", got)
	}
}
