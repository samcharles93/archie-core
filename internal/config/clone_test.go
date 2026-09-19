package config

import (
	"testing"
)

// TestConfigCloneDeepCopiesReferenceFields pins that Clone returns a
// value sharing no memory with the original: mutating the clone's maps
// and slices must not touch the original. A shallow copy here would
// silently let a failed overlay decode mutate the published snapshot.
func TestConfigCloneDeepCopiesReferenceFields(t *testing.T) {
	enabled := true
	orig := Config{
		Models:       map[string]string{"builder": "m"},
		ModelLimits:  map[string]ModelLimits{"m": {ContextWindow: 128_000}},
		Repos:        []Repo{{Gate: [][]string{{"task", "check"}}, Protect: []string{"p"}}},
		DiffCapLines: new(400),
		Identities: []IdentityConfig{{
			Models:       map[string]string{"planner": "m2"},
			DiffCapLines: new(50),
			Repos:        []Repo{{Gate: [][]string{{"go", "vet"}}}},
		}},
		Dispatch: Dispatch{Labels: map[string]string{"q": "archie:q"}},
		Tools: ToolsConfig{
			MCPServers: []MCPServer{{Headers: map[string]string{"A": "b"}, Args: []string{"x"}}},
			WebFetch:   WebFetchConfig{Enabled: &enabled},
		},
		Chat:        ChatConfig{Telegram: TelegramConfig{AllowedUserIDs: []int64{1}}},
		LegacyAgent: LegacyAgent{Env: []string{"HOME"}},
		Extra:       map[string]any{"custom": 1},
		Bindings:    BindingsConfig{PreviousEncryptionKeys: []SecretRef{{Engine: "env", Key: "K0"}}},
		Image: ImageConfig{
			Hosted: map[string]ImageHostedProvider{"openai": {Enabled: true}},
			Local:  map[string]ImageLocalProvider{"sdxl": {Enabled: true}},
		},
		Services: Services{
			ServiceNameState: {Target: "127.0.0.1:9090", TargetToken: "secret"},
		},
	}

	got := orig.Clone()
	got.Models["builder"] = "changed"
	got.ModelLimits["m"] = ModelLimits{ContextWindow: 1}
	got.Repos[0].Gate[0][0] = "changed"
	got.Repos[0].Protect[0] = "changed"
	got.Identities[0].Models["planner"] = "changed"
	got.Identities[0].Repos[0].Gate[0][0] = "changed"
	got.Dispatch.Labels["q"] = "changed"
	got.Tools.MCPServers[0].Headers["A"] = "changed"
	got.Tools.MCPServers[0].Args[0] = "changed"
	got.Chat.Telegram.AllowedUserIDs[0] = 99
	got.LegacyAgent.Env[0] = "changed"
	got.Extra["custom"] = 2
	got.Bindings.PreviousEncryptionKeys[0] = SecretRef{Engine: "env", Key: "changed"}
	*got.Tools.WebFetch.Enabled = false
	*got.DiffCapLines = 1
	*got.Identities[0].DiffCapLines = 2
	got.Image.Hosted["openai"] = ImageHostedProvider{Enabled: false}
	got.Image.Local["sdxl"] = ImageLocalProvider{Enabled: false}
	got.Services[ServiceNameState] = ServiceConnection{Target: "changed"}

	if orig.Models["builder"] != "m" {
		t.Error("Models map is shared")
	}
	if orig.ModelLimits["m"].ContextWindow != 128_000 {
		t.Error("ModelLimits map is shared")
	}
	if orig.Repos[0].Gate[0][0] != "task" || orig.Repos[0].Protect[0] != "p" {
		t.Error("Repo nested slices are shared")
	}
	if orig.Identities[0].Models["planner"] != "m2" || orig.Identities[0].Repos[0].Gate[0][0] != "go" {
		t.Error("Identity nested fields are shared")
	}
	if orig.Dispatch.Labels["q"] != "archie:q" {
		t.Error("Dispatch.Labels map is shared")
	}
	if orig.Tools.MCPServers[0].Headers["A"] != "b" || orig.Tools.MCPServers[0].Args[0] != "x" {
		t.Error("MCP server fields are shared")
	}
	if orig.Chat.Telegram.AllowedUserIDs[0] != 1 {
		t.Error("Telegram.AllowedUserIDs is shared")
	}
	if orig.LegacyAgent.Env[0] != "HOME" {
		t.Error("Agent.Env is shared")
	}
	if orig.Extra["custom"] != 1 {
		t.Error("Extra map is shared")
	}
	if orig.Bindings.PreviousEncryptionKeys[0].Key != "K0" {
		t.Error("Bindings.PreviousEncryptionKeys is shared")
	}
	if orig.DiffCap() != 400 {
		t.Errorf("DiffCapLines pointer is shared: orig.DiffCap() = %d, want 400", orig.DiffCap())
	}
	if got := orig.Identities[0].DiffCapLines; got == nil || *got != 50 {
		t.Errorf("Identities[0].DiffCapLines pointer is shared: got %v, want 50", got)
	}
	if !*orig.Tools.WebFetch.Enabled {
		t.Error("WebFetch.Enabled pointer is shared")
	}
	if !orig.Image.Hosted["openai"].Enabled {
		t.Error("Image.Hosted map is shared")
	}
	if !orig.Image.Local["sdxl"].Enabled {
		t.Error("Image.Local map is shared")
	}
	if orig.Services[ServiceNameState].TargetToken != "secret" {
		t.Error("Services map is shared")
	}
}
