package configuration

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

// A minimal config that satisfies every check validate applies without
// relying on applyDefaults -- Validate documents that it does not apply
// defaults, so this fixture fills in every field a default would otherwise
// have supplied.
func minimalValidConfig() config.Config {
	return config.Config{
		BotUser:      "archie-bot",
		PollInterval: config.Duration(60 * time.Second),
		Agent:        config.Agent{Mode: "inprocess"},
		Dispatch: config.Dispatch{
			Trigger: "assignee",
		},
		Forge: config.Forge{Type: "none"},
	}
}

// TestForgeDisabled pins the single definition of the disabled-forge
// predicate: cmd/archied's resolveForge calls ForgeDisabled instead of
// maintaining its own copy, so any alias added or removed here must hold
// for both config validation and daemon startup. The table deliberately
// includes a case-variant alias and the empty string to catch a future
// implementation that lowercases or matches prefixes instead of comparing
// the exact aliases.
func TestForgeDisabled(t *testing.T) {
	tests := []struct {
		name string
		typ  string
		want bool
	}{
		{name: "none", typ: "none", want: true},
		{name: "off", typ: "off", want: true},
		{name: "disabled", typ: "disabled", want: true},
		{name: "github", typ: "github", want: false},
		{name: "gitea", typ: "gitea", want: false},
		{name: "empty string", typ: "", want: false},
		{name: "case variant is not an alias", typ: "None", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ForgeDisabled(tc.typ); got != tc.want {
				t.Errorf("ForgeDisabled(%q) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

func TestValidate_IsCallableOutsideThePackage(t *testing.T) {
	cfg := minimalValidConfig()
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate(minimal valid config) = %v, want nil", err)
	}
}

func TestValidate_RejectsTheSameProblemsAsLoaderLoad(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(cfg *config.Config)
		wantErr bool
	}{
		{
			name:    "valid config",
			mutate:  func(cfg *config.Config) {},
			wantErr: false,
		},
		{
			name:    "missing bot_user with no identities",
			mutate:  func(cfg *config.Config) { cfg.BotUser = "" },
			wantErr: true,
		},
		{
			name:    "unrecognised agent.mode",
			mutate:  func(cfg *config.Config) { cfg.Agent.Mode = "not-a-real-mode" },
			wantErr: true,
		},
		{
			name:    "unrecognised dispatch.trigger",
			mutate:  func(cfg *config.Config) { cfg.Dispatch.Trigger = "not-a-real-trigger" },
			wantErr: true,
		},
		{
			name:    "unrecognised forge.type",
			mutate:  func(cfg *config.Config) { cfg.Forge.Type = "not-a-real-forge" },
			wantErr: true,
		},
		{
			name:    "negative poll_interval",
			mutate:  func(cfg *config.Config) { cfg.PollInterval = config.Duration(-5 * time.Second) },
			wantErr: true,
		},
		{
			name:    "zero poll_interval",
			mutate:  func(cfg *config.Config) { cfg.PollInterval = 0 },
			wantErr: true,
		},
		{
			name: "provider base_url carries userinfo",
			mutate: func(cfg *config.Config) {
				cfg.Providers = map[string]config.Provider{
					"acme": {BaseURL: "https://user:pass@example.com"},
				}
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			tc.mutate(&cfg)
			err := Validate(&cfg)
			if tc.wantErr && err == nil {
				t.Fatal("Validate() = nil, want an error")
			}
			if tc.wantErr && !errors.Is(err, ErrInvalidInput) {
				t.Errorf("Validate() = %v, want it to wrap ErrInvalidInput", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

// Validate must run the exact same checks Loader.File applies internally --
// this pins that a config Validate accepts on its own, once the same
// defaults Loader.File would apply are filled in by hand, is also accepted
// when it goes through the real file-loading path.
func TestValidate_AgreesWithLoaderLoad(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.toml"
	const doc = `
bot_user = "archie-bot"

[agent]
mode = "inprocess"

[dispatch]
trigger = "assignee"

[forge]
type = "none"
`
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := minimalValidConfig()
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate(in-memory) = %v, want nil", err)
	}

	if _, err := New(nil).File(path); err != nil {
		t.Fatalf("Loader.File(equivalent config on disk) = %v, want nil", err)
	}
}
