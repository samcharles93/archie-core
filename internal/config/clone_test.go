package config

import "testing"

// TestConfigCloneDeepCopiesReferenceFields pins that Clone returns a
// value sharing no memory with the original: mutating the clone's maps
// and slices must not touch the original. A shallow copy here would
// silently let a failed overlay decode mutate the published snapshot.
func TestConfigCloneDeepCopiesReferenceFields(t *testing.T) {
	enabled := true
	orig := Config{
		Models: map[string]string{"builder": "m"},
		Repos:  []Repo{{Gate: [][]string{{"task", "check"}}, Protect: []string{"p"}}},
		Identities: []IdentityConfig{{
			Models: map[string]string{"planner": "m2"},
			Repos:  []Repo{{Gate: [][]string{{"go", "vet"}}}},
		}},
		Dispatch: Dispatch{Labels: map[string]string{"q": "archie:q"}},
		Tools: ToolsConfig{
			MCPServers: []MCPServer{{Headers: map[string]string{"A": "b"}, Args: []string{"x"}}},
			WebFetch:   WebFetchConfig{Enabled: &enabled},
		},
		Memory:      MemoryConfig{ProviderConfig: map[string]string{"k": "v"}},
		Chat:        ChatConfig{Telegram: TelegramConfig{AllowedUserIDs: []int64{1}}},
		LegacyAgent: LegacyAgent{Env: []string{"HOME"}},
		Extra:       map[string]any{"custom": 1},
	}

	got := orig.Clone()
	got.Models["builder"] = "changed"
	got.Repos[0].Gate[0][0] = "changed"
	got.Repos[0].Protect[0] = "changed"
	got.Identities[0].Models["planner"] = "changed"
	got.Identities[0].Repos[0].Gate[0][0] = "changed"
	got.Dispatch.Labels["q"] = "changed"
	got.Tools.MCPServers[0].Headers["A"] = "changed"
	got.Tools.MCPServers[0].Args[0] = "changed"
	got.Memory.ProviderConfig["k"] = "changed"
	got.Chat.Telegram.AllowedUserIDs[0] = 99
	got.LegacyAgent.Env[0] = "changed"
	got.Extra["custom"] = 2
	*got.Tools.WebFetch.Enabled = false

	if orig.Models["builder"] != "m" {
		t.Error("Models map is shared")
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
	if orig.Memory.ProviderConfig["k"] != "v" {
		t.Error("Memory.ProviderConfig is shared")
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
	if !*orig.Tools.WebFetch.Enabled {
		t.Error("WebFetch.Enabled pointer is shared")
	}
}
