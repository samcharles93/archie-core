package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDispatchDefaults(t *testing.T) {
	// Simulate a config with no [dispatch] section — all fields zero.
	var d Dispatch
	d.Labels = map[string]string{}

	// Per-key defaults (mimicking Load's loop).
	for k, v := range dispatchLabelDefaults {
		if d.Labels[k] == "" {
			d.Labels[k] = v
		}
	}
	if d.Trigger == "" {
		d.Trigger = "assignee"
	}
	if d.AckReaction == "" {
		d.AckReaction = "eyes"
	}

	// StateLabel should return the legacy constants.
	if got := d.StateLabel("queued"); got != "archie:queued" {
		t.Errorf("queued: got %q, want archie:queued", got)
	}
	if got := d.StateLabel("working"); got != "archie:working" {
		t.Errorf("working: got %q, want archie:working", got)
	}
	if got := d.StateLabel("waiting"); got != "archie:waiting" {
		t.Errorf("waiting: got %q, want archie:waiting", got)
	}
	if got := d.StateLabel("pr"); got != "archie:pr" {
		t.Errorf("pr: got %q, want archie:pr", got)
	}
	if got := d.StateLabel("parked"); got != "archie:parked" {
		t.Errorf("parked: got %q, want archie:parked", got)
	}

	// Unknown state returns empty.
	if got := d.StateLabel("nonexistent"); got != "" {
		t.Errorf("nonexistent: got %q, want empty", got)
	}

	// AckReaction default.
	if d.AckReaction != "eyes" {
		t.Errorf("AckReaction: got %q, want eyes", d.AckReaction)
	}

	// Trigger default.
	if d.Trigger != "assignee" {
		t.Errorf("Trigger: got %q, want assignee", d.Trigger)
	}
}

func TestDispatchCustomLabels(t *testing.T) {
	d := Dispatch{
		Trigger:     "label",
		AckReaction: "",
		Labels: map[string]string{
			"queued":  "bot:queued",
			"working": "bot:working",
			"parked":  "bot:parked",
			// "waiting" and "pr" intentionally missing — should fall back.
		},
	}

	// Custom labels.
	if got := d.StateLabel("queued"); got != "bot:queued" {
		t.Errorf("queued: got %q, want bot:queued", got)
	}
	if got := d.StateLabel("parked"); got != "bot:parked" {
		t.Errorf("parked: got %q, want bot:parked", got)
	}

	// Missing keys fall back to defaults.
	if got := d.StateLabel("waiting"); got != "archie:waiting" {
		t.Errorf("waiting: got %q, want archie:waiting (fallback)", got)
	}
	if got := d.StateLabel("pr"); got != "archie:pr" {
		t.Errorf("pr: got %q, want archie:pr (fallback)", got)
	}

	// Empty AckReaction.
	if d.AckReaction != "" {
		t.Errorf("AckReaction: got %q, want empty", d.AckReaction)
	}

	// LabelValues returns all six state labels.
	vals := d.LabelValues()
	if len(vals) != 6 {
		t.Errorf("LabelValues: got %d values, want 6: %v", len(vals), vals)
	}
	seen := map[string]bool{}
	for _, v := range vals {
		seen[v] = true
	}
	for _, want := range []string{"bot:queued", "bot:working", "bot:parked", "archie:waiting", "archie:pr", "archie:dead"} {
		if !seen[want] {
			t.Errorf("LabelValues missing %q", want)
		}
	}
}

func TestDispatchLabelValuesDedup(t *testing.T) {
	// When two state keys map to the same label string, LabelValues
	// should deduplicate.
	d := Dispatch{
		Labels: map[string]string{
			"queued":  "bot:queued",
			"working": "bot:queued", // same as queued
			"parked":  "bot:queued", // same again
		},
	}
	vals := d.LabelValues()
	// Should have "bot:queued" plus fallbacks for waiting, pr, and dead.
	if len(vals) != 4 {
		t.Errorf("LabelValues: got %d values, want 4: %v", len(vals), vals)
	}
}

func TestLoadDispatchAckReaction(t *testing.T) {
	tests := []struct {
		name     string
		dispatch string
		wantAck  string
	}{
		{name: "default", wantAck: "eyes"},
		{name: "disabled", dispatch: "\n[dispatch]\nack_reaction = \"off\"\n", wantAck: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"archie\"\n" + tt.dispatch + "\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Dispatch.AckReaction != tt.wantAck {
				t.Errorf("AckReaction: got %q, want %q", cfg.Dispatch.AckReaction, tt.wantAck)
			}
		})
	}
}

func TestLoadRejectsInvalidConfigEnumsAndGlobs(t *testing.T) {
	tests := []struct {
		name  string
		extra string
	}{
		{name: "dispatch trigger", extra: "\n[dispatch]\ntrigger = \"labels\"\n"},
		{name: "agent mode", extra: "\n[agent]\nmode = \"remote\"\n"},
		{name: "agent command", extra: "\n[agent]\nmode = \"subprocess\"\ncommand = \"   \"\n"},
		{name: "agent env", extra: "\n[agent]\nenv = [\"TOKEN=value\"]\n"},
		{name: "provider userinfo", extra: "\n[providers.openai]\nclass = \"openai\"\nbase_url = \"https://token@example.com/v1\"\n"},
		{name: "provider query secret", extra: "\n[providers.openai]\nclass = \"openai\"\nbase_url = \"https://example.com/v1?api_key=secret\"\n"},
		{name: "test glob", extra: "\n[[repos]]\nowner = \"acme\"\nname = \"app\"\ntest_glob = \"[\"\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"archie\"\n"
			if tt.name != "test glob" {
				contents += "\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			}
			contents += tt.extra
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := Load(path); err == nil {
				t.Fatal("Load() succeeded, want validation error")
			}
		})
	}
}

func TestLoadAgentDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name        string
		agent       string
		wantMode    string
		wantCommand string
		wantEnv     []string
	}{
		{name: "defaults", wantMode: "inprocess", wantCommand: "archie-agent"},
		{
			name: "subprocess", agent: "\n[agent]\nmode = \"subprocess\"\ncommand = \"/opt/archie-agent\"\nenv = [\"GOCACHE\"]\n",
			wantMode: "subprocess", wantCommand: "/opt/archie-agent", wantEnv: []string{"GOCACHE"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			contents := "bot_user = \"archie\"\n" + tt.agent + "\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Agent.Mode != tt.wantMode || cfg.Agent.Command != tt.wantCommand || strings.Join(cfg.Agent.Env, ",") != strings.Join(tt.wantEnv, ",") {
				t.Fatalf("agent config = %#v", cfg.Agent)
			}
		})
	}
}

func TestExampleConfigLoads(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "config.example.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Forge.Host != "https://github.com" {
		t.Errorf("Forge.Host: got %q", cfg.Forge.Host)
	}
	if cfg.Dispatch.AckReaction != "eyes" {
		t.Errorf("Dispatch.AckReaction: got %q", cfg.Dispatch.AckReaction)
	}
	if len(cfg.Repos) != 1 || cfg.Repos[0].ResolvedTestGlob() != "*_test.go" {
		t.Errorf("Repos: got %+v", cfg.Repos)
	}
}

func TestEcosystemDefaults(t *testing.T) {
	// Empty ecosystem → "go" (backward compat).
	r := Repo{}
	if got := r.effectiveEcosystem(); got != "go" {
		t.Errorf("effectiveEcosystem empty: got %q, want go", got)
	}
	if got := r.ResolvedTestGlob(); got != "*_test.go" {
		t.Errorf("ResolvedTestGlob empty: got %q, want *_test.go", got)
	}
	pre := r.ResolvedPreflight()
	if len(pre) != 1 || pre[0][0] != "go" || pre[0][1] != "version" {
		t.Errorf("ResolvedPreflight empty: got %v, want [[go version]]", pre)
	}

	// Explicit ecosystem.
	r2 := Repo{Ecosystem: "python"}
	if got := r2.ResolvedTestGlob(); got != "test_*.py" {
		t.Errorf("ResolvedTestGlob python: got %q, want test_*.py", got)
	}
	pre2 := r2.ResolvedPreflight()
	if len(pre2) != 1 || pre2[0][0] != "python" || pre2[0][1] != "--version" {
		t.Errorf("ResolvedPreflight python: got %v, want [[python --version]]", pre2)
	}
}

func TestEcosystemOverrides(t *testing.T) {
	// Explicit Preflight and TestGlob always win over ecosystem.
	r := Repo{
		Ecosystem: "go",
		Preflight: [][]string{{"make", "check"}},
		TestGlob:  "spec_*.py",
	}
	if got := r.ResolvedTestGlob(); got != "spec_*.py" {
		t.Errorf("ResolvedTestGlob override: got %q, want spec_*.py", got)
	}
	pre := r.ResolvedPreflight()
	if len(pre) != 1 || pre[0][0] != "make" || pre[0][1] != "check" {
		t.Errorf("ResolvedPreflight override: got %v, want [[make check]]", pre)
	}
}

func TestEcosystemCustom(t *testing.T) {
	// "custom" ecosystem has no defaults — preflight empty, test glob empty.
	r := Repo{Ecosystem: "custom"}
	if got := r.ResolvedTestGlob(); got != "" {
		t.Errorf("ResolvedTestGlob custom: got %q, want empty", got)
	}
	if got := r.ResolvedPreflight(); got != nil {
		t.Errorf("ResolvedPreflight custom: got %v, want nil", got)
	}
}
