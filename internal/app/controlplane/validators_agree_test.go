package controlplane

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// TestResourceValidatorsRejectWhatEffectiveValidationRejects ties the two rule
// sets that guard the same fields. A control-plane resource is seeded from the
// file document and then replaces it wholesale (Client.RuntimeConfig), so a
// value the file path accepts must not be storable through the resource, and a
// value the resource accepts must not stop the daemon's validation of the
// effective document. Guards that live only on one side are how a stale TOML
// value becomes a database value the operator cannot fix by editing config.toml.
//
// Each case mutates the same field the resource carries, so the two halves
// cannot drift apart without failing here.
func TestResourceValidatorsRejectWhatEffectiveValidationRejects(t *testing.T) {
	tests := []struct {
		name    string
		kind    string
		value   any
		mutate  func(cfg *config.Config)
		wantErr bool
	}{
		{
			name:    "valid scheduling policy",
			kind:    SchedulingPolicyKind,
			value:   map[string]any{"poll_interval": "1m0s", "max_retries": 0, "dispatch": map[string]any{"trigger": "assignee"}},
			mutate:  func(cfg *config.Config) {},
			wantErr: false,
		},
		{
			name:    "unrecognised dispatch trigger",
			kind:    SchedulingPolicyKind,
			value:   map[string]any{"poll_interval": "1m0s", "max_retries": 0, "dispatch": map[string]any{"trigger": "labels"}},
			mutate:  func(cfg *config.Config) { cfg.Dispatch.Trigger = "labels" },
			wantErr: true,
		},
		{
			name:    "negative poll interval",
			kind:    SchedulingPolicyKind,
			value:   map[string]any{"poll_interval": "-5s", "max_retries": 0, "dispatch": map[string]any{"trigger": "assignee"}},
			mutate:  func(cfg *config.Config) { cfg.PollInterval = config.Duration(-5 * time.Second) },
			wantErr: true,
		},
		{
			name:    "valid repository policies",
			kind:    RepositoryPoliciesKind,
			value:   []config.Repo{{Owner: "acme", Name: "app", TestGlob: "**/*_test.go"}},
			mutate:  func(cfg *config.Config) {},
			wantErr: false,
		},
		{
			name:    "malformed test glob",
			kind:    RepositoryPoliciesKind,
			value:   []config.Repo{{Owner: "acme", Name: "app", TestGlob: "["}},
			mutate:  func(cfg *config.Config) { cfg.Repos = []config.Repo{{Owner: "acme", Name: "app", TestGlob: "["}} },
			wantErr: true,
		},
		{
			name:    "valid container runtime policies",
			kind:    ContainerRuntimePoliciesKind,
			value:   config.ContainerConfig{Image: "archie-agent:test", PullPolicy: "missing"},
			mutate:  func(cfg *config.Config) {},
			wantErr: false,
		},
		{
			name:    "container runtime policies without an image",
			kind:    ContainerRuntimePoliciesKind,
			value:   config.ContainerConfig{PullPolicy: "missing"},
			mutate:  func(cfg *config.Config) { cfg.Containers.Image = "" },
			wantErr: true,
		},
		{
			name:    "negative container volume ttl",
			kind:    ContainerRuntimePoliciesKind,
			value:   config.ContainerConfig{Image: "archie-agent:test", VolumeTTL: config.Duration(-time.Minute)},
			mutate:  func(cfg *config.Config) { cfg.Containers.VolumeTTL = config.Duration(-time.Minute) },
			wantErr: true,
		},
		{
			name:  "provider base URL carrying userinfo",
			kind:  ProviderSettingsKind,
			value: map[string]any{"acme": map[string]any{"class": "openai", "base_url": "https://user:pass@example.com"}},
			mutate: func(cfg *config.Config) {
				cfg.Providers = map[string]config.Provider{"acme": {Class: "openai", BaseURL: "https://user:pass@example.com"}}
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfigForValidation()
			tt.mutate(&cfg)
			configErr := configuration.Validate(&cfg)

			encoded, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatal(err)
			}
			resourceErr := decodeResource(t, tt.kind, encoded)

			if !tt.wantErr {
				if configErr != nil {
					t.Errorf("configuration.Validate = %v, want nil for a valid value", configErr)
				}
				if resourceErr != nil {
					t.Errorf("%s Decode = %v, want nil for a valid value", tt.kind, resourceErr)
				}
				return
			}
			if configErr == nil {
				t.Errorf("configuration.Validate accepted the value, want the effective document to reject it")
			}
			if resourceErr == nil {
				t.Errorf("%s accepted the value, want the resource validator to reject what the effective document rejects", tt.kind)
			}
		})
	}
}

// decodeResource runs the resource validator exactly as ImportConfig does when
// it seeds a kind from the file config (registry.go), against the production
// server the composition roots build (testServer).
func decodeResource(t *testing.T, kind string, encoded []byte) error {
	t.Helper()
	_, err := testServer(t, nil).definitions[kind].Decode(encoded)
	return err
}

// validConfigForValidation is a document every check passes, so each case above
// fails only for the value it changes -- same fixture the configuration package
// uses, restated here because Validate documents that it does not apply
// defaults.
func validConfigForValidation() config.Config {
	return config.Config{
		BotUser:      "archie-bot",
		PollInterval: config.Duration(60 * time.Second),
		Dispatch:     config.Dispatch{Trigger: "assignee"},
		Forge:        config.Forge{Type: "none"},
		Containers:   config.ContainerConfig{Image: "ghcr.io/samcharles93/archie-agent:latest"},
		Services:     config.Services{config.ServiceNameGateway: {Target: "127.0.0.1:8585"}},
	}
}
