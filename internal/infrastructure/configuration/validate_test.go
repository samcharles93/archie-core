package configuration

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/secret"
)

// A minimal config that satisfies every check validate applies without
// relying on applyDefaults -- Validate documents that it does not apply
// defaults, so this fixture fills in every field a default would otherwise
// have supplied.
func minimalValidConfig() config.Config {
	return config.Config{
		BotUser:      "archie-bot",
		PollInterval: config.Duration(60 * time.Second),
		Dispatch: config.Dispatch{
			Trigger: "assignee",
		},
		Forge: config.Forge{Type: "none"},
		Containers: config.ContainerConfig{
			Image: "ghcr.io/samcharles93/archie-agent:latest",
		},
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
			name:    "unrecognised dispatch.trigger",
			mutate:  func(cfg *config.Config) { cfg.Dispatch.Trigger = "not-a-real-trigger" },
			wantErr: true,
		},
		{
			// GH#445: an empty label matches every open issue via the forge
			// API's issues-list filter, so a "label" trigger with no label
			// set must fail validation rather than queue the whole repo.
			name: "label trigger with empty label",
			mutate: func(cfg *config.Config) {
				cfg.Dispatch.Trigger = "label"
				cfg.Label = ""
			},
			wantErr: true,
		},
		{
			name: "either trigger with empty label",
			mutate: func(cfg *config.Config) {
				cfg.Dispatch.Trigger = "either"
				cfg.Label = ""
			},
			wantErr: true,
		},
		{
			name: "label trigger with a label set",
			mutate: func(cfg *config.Config) {
				cfg.Dispatch.Trigger = "label"
				cfg.Label = "archie"
			},
			wantErr: false,
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
		{
			name:    "unrecognised memory.engine",
			mutate:  func(cfg *config.Config) { cfg.Memory.Engine = "not-a-real-engine" },
			wantErr: true,
		},
		{
			name:    "recognised memory.engine",
			mutate:  func(cfg *config.Config) { cfg.Memory.Engine = "builtin" },
			wantErr: false,
		},
		{
			name:    "empty memory.engine is not unrecognised",
			mutate:  func(cfg *config.Config) { cfg.Memory.Engine = "" },
			wantErr: false,
		},
		{
			name:    "negative capture.retention",
			mutate:  func(cfg *config.Config) { cfg.Capture.Retention = config.Duration(-time.Hour) },
			wantErr: true,
		},
		{
			name:    "negative capture.max_events",
			mutate:  func(cfg *config.Config) { cfg.Capture.MaxEvents = -1 },
			wantErr: true,
		},
		{
			name:    "negative capture.max_body_bytes",
			mutate:  func(cfg *config.Config) { cfg.Capture.MaxBodyBytes = -1 },
			wantErr: true,
		},
		{
			name:    "negative capture.rate_per_second",
			mutate:  func(cfg *config.Config) { cfg.Capture.RatePerSecond = -1 },
			wantErr: true,
		},
		{
			name:    "negative capture.rate_burst",
			mutate:  func(cfg *config.Config) { cfg.Capture.RateBurst = -1 },
			wantErr: true,
		},
		{
			name:    "zero capture fields are valid (defaults fill them)",
			mutate:  func(cfg *config.Config) {},
			wantErr: false,
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

// TestValidateNATS pins the nats mode contract from docs/prds/embedded-nats.md:
// an unset mode resolves from url without mutating cfg, external requires url,
// embedded/off forbid a url, and full-task workers work with either embedded
// or external broker deployment.
func TestValidateNATS(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		url     string
		wantErr bool
	}{
		{name: "unset mode with url resolves external", url: "nats://localhost:4222"},
		{name: "unset mode without url resolves embedded"},
		{name: "embedded with no url", mode: config.NATSModeEmbedded},
		{name: "removed off mode", mode: "off", wantErr: true},
		{name: "external with url", mode: config.NATSModeExternal, url: "nats://localhost:4222"},
		{name: "external without url", mode: config.NATSModeExternal, wantErr: true},
		{name: "embedded with url", mode: config.NATSModeEmbedded, url: "nats://localhost:4222", wantErr: true},
		{name: "removed off mode with url", mode: "off", url: "nats://localhost:4222", wantErr: true},
		{name: "unknown mode", mode: "not-a-mode", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			cfg.NATS = config.NATSConfig{Mode: tc.mode, URL: tc.url}
			err := validateNATS(&cfg)
			if tc.wantErr && err == nil {
				t.Fatal("validateNATS() = nil, want an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateNATS() = %v, want nil", err)
			}
		})
	}
}

// TestValidateContainersResolvesNATSMode pins that validateContainers reads
// the *resolved* nats mode, not the raw field: a hand-built config with
// containers enabled and a url but no explicit mode must validate the same way
// the loader's on-disk form does (url implies external), rather than diverge
// because validateContainers saw an empty mode.
func TestValidateContainersSupportsBothBrokerDeployments(t *testing.T) {
	base := func() config.Config {
		cfg := minimalValidConfig()
		cfg.Containers = config.ContainerConfig{Image: "archie-agent:latest"}
		return cfg
	}

	t.Run("unset mode with url resolves external", func(t *testing.T) {
		cfg := base()
		cfg.NATS.URL = "nats://localhost:4222"
		if err := Validate(&cfg); err != nil {
			t.Fatalf("Validate() = %v, want nil (unset mode + url resolves external)", err)
		}
	})

	t.Run("embedded mode supports containers", func(t *testing.T) {
		cfg := base()
		cfg.NATS.Mode = config.NATSModeEmbedded
		if err := Validate(&cfg); err != nil {
			t.Fatalf("Validate() = %v, want nil for managed containers with embedded nats", err)
		}
	})
}

// TestValidateForgeIntake pins the forge.intake contract: an unset intake
// resolves to poll without mutating cfg, and webhook/both require a webhook
// secret and listen address.
func TestValidateForgeIntake(t *testing.T) {
	tests := []struct {
		name     string
		intake   string
		secret   secret.SecretRef
		addr     string
		natsMode string
		wantErr  bool
	}{
		{name: "unset resolves poll"},
		{name: "poll is valid", intake: config.ForgeIntakePoll},
		{name: "webhook requires secret", intake: config.ForgeIntakeWebhook, addr: "0.0.0.0:8645", wantErr: true},
		{name: "webhook requires addr", intake: config.ForgeIntakeWebhook, secret: secret.SecretRef{Engine: "env", Key: "SECRET"}, wantErr: true},
		{name: "webhook with secret and addr is valid", intake: config.ForgeIntakeWebhook, secret: secret.SecretRef{Engine: "env", Key: "SECRET"}, addr: "0.0.0.0:8645"},
		{name: "both with secret and addr is valid", intake: config.ForgeIntakeBoth, secret: secret.SecretRef{Engine: "env", Key: "SECRET"}, addr: "0.0.0.0:8645"},
		{name: "unknown intake", intake: "not-a-mode", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			cfg.Forge.Intake = tc.intake
			cfg.Forge.WebhookSecret = tc.secret
			cfg.Forge.WebhookAddr = tc.addr
			if tc.natsMode != "" {
				cfg.NATS.Mode = tc.natsMode
			}
			err := validateForgeIntake(&cfg)
			if tc.wantErr && err == nil {
				t.Fatal("validateForgeIntake() = nil, want an error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateForgeIntake() = %v, want nil", err)
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

// TestValidateMemoryNamesTheValidSet pins the AC this satisfies: an
// unknown memory.engine is rejected with a clear error naming the valid
// set, in the same style forge.type already uses.
func TestValidateMemoryNamesTheValidSet(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.Memory.Engine = "not-a-real-engine"

	err := validateMemory(&cfg)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("validateMemory(unknown engine) = %v, want wrapping ErrInvalidInput", err)
	}
	if got, want := err.Error(), `memory.engine "not-a-real-engine" (want builtin)`; !strings.Contains(got, want) {
		t.Errorf("error text = %q, want it to contain %q", got, want)
	}
}
