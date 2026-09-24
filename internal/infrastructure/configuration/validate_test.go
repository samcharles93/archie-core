package configuration

import (
	"errors"
	"os"
	"path/filepath"
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
		Services: config.Services{
			config.ServiceNameGateway: {Target: "127.0.0.1:8585"},
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

// TestValidate_RejectsTheSameProblemsAsLoaderLoad pins the direction that still
// holds after archie-core-i3qm split the bootstrap checks from the effective
// ones: every problem the load path rejects, Validate rejects too. Validate
// additionally rejects the settings the control plane owns, which the load path
// must let through (validateBootstrap) so a stale TOML value cannot fail a
// process's startup. The name is kept from when the two check sets were equal.
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

// TestValidateForgeIntakeRejectsIdentityIntake pins that an intake setting on
// a [[identities]] entry is refused instead of accepted-and-ignored.
// IdentityConfig.Forge is the same type as the root [forge] block, so
// identities[N].forge.intake, .webhook_secret and .webhook_addr all decode --
// but every intake read is of the root block, so before this check they
// parsed, reached no code path, and were never even validated. A setting an
// operator can spell that nothing reads is a lie in the config file.
func TestValidateForgeIntakeRejectsIdentityIntake(t *testing.T) {
	// identity builds one entry whose forge is otherwise valid, so a
	// rejection can only come from the field the case sets.
	identity := func(mutate func(*config.Forge)) config.IdentityConfig {
		forge := config.Forge{
			Type:  "github",
			Token: config.SecretRef{Engine: "env", Key: "ARCHIE_GITHUB_TOKEN"},
		}
		if mutate != nil {
			mutate(&forge)
		}
		return config.IdentityConfig{Name: "personal", BotUser: "archie-personal", Forge: forge}
	}

	tests := []struct {
		name       string
		identities []config.IdentityConfig
		// want lists fragments the error must carry; empty means no error.
		want []string
	}{
		{
			name:       "unset intake on an identity is valid",
			identities: []config.IdentityConfig{identity(nil)},
		},
		{
			name: "poll intake on an identity is valid",
			identities: []config.IdentityConfig{identity(func(f *config.Forge) {
				f.Intake = config.ForgeIntakePoll
			})},
		},
		{
			name: "webhook intake on an identity is rejected with the identity, setting, and reason",
			identities: []config.IdentityConfig{identity(func(f *config.Forge) {
				f.Intake = config.ForgeIntakeWebhook
			})},
			want: []string{`identities[0] ("personal").forge.intake "webhook"`, "per-identity webhook intake is not implemented"},
		},
		{
			name: "both intake on an identity is rejected with the identity, setting, and reason",
			identities: []config.IdentityConfig{identity(func(f *config.Forge) {
				f.Intake = config.ForgeIntakeBoth
			})},
			want: []string{`identities[0] ("personal").forge.intake "both"`, "per-identity webhook intake is not implemented"},
		},
		{
			name: "an unknown intake on an identity names the accepted value",
			identities: []config.IdentityConfig{identity(func(f *config.Forge) {
				f.Intake = "sometimes"
			})},
			want: []string{`identities[0] ("personal").forge.intake "sometimes"`, "want poll"},
		},
		{
			name: "webhook_secret on an identity is rejected",
			identities: []config.IdentityConfig{identity(func(f *config.Forge) {
				f.WebhookSecret = config.SecretRef{Engine: "env", Key: "ARCHIE_WEBHOOK_SECRET"}
			})},
			want: []string{`identities[0] ("personal").forge.webhook_secret`, "per-identity webhook intake is not implemented"},
		},
		{
			name: "webhook_addr on an identity is rejected",
			identities: []config.IdentityConfig{identity(func(f *config.Forge) {
				f.WebhookAddr = "0.0.0.0:8645"
			})},
			want: []string{`identities[0] ("personal").forge.webhook_addr`, "per-identity webhook intake is not implemented"},
		},
		{
			// The offender is the second entry: the message must locate it, not
			// report "an identity" for a file whose first identity is fine.
			name: "the rejected identity is the one named, not the first",
			identities: []config.IdentityConfig{
				identity(nil),
				{
					Name:    "work",
					BotUser: "archie-work",
					Forge: config.Forge{
						Type:   "gitea",
						Intake: config.ForgeIntakeWebhook,
						Token:  config.SecretRef{Engine: "env", Key: "ARCHIE_GITEA_TOKEN"},
					},
				},
			},
			want: []string{`identities[1] ("work").forge.intake "webhook"`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			cfg.Identities = tc.identities
			requireValidateError(t, validateForgeIntake(&cfg), tc.want)
		})
	}
}

// TestValidateForgeIntakeRejectsRootWebhookIntakeWithIdentities pins the other
// half of the same lie: [forge].intake = "webhook"/"both" alongside
// [[identities]] used to start the daemon, which logged "forge webhook
// disabled: multi-identity deployments are not supported yet" and polled
// instead. Failing closed at validation replaces that silent downgrade.
func TestValidateForgeIntakeRejectsRootWebhookIntakeWithIdentities(t *testing.T) {
	identity := config.IdentityConfig{
		Name:    "personal",
		BotUser: "archie-personal",
		Forge: config.Forge{
			Type:  "github",
			Token: config.SecretRef{Engine: "env", Key: "ARCHIE_GITHUB_TOKEN"},
		},
	}

	tests := []struct {
		name       string
		intake     string
		identities []config.IdentityConfig
		want       []string
	}{
		{
			name:       "webhook intake plus identities is rejected",
			intake:     config.ForgeIntakeWebhook,
			identities: []config.IdentityConfig{identity},
			want:       []string{`forge.intake "webhook"`, "alongside [[identities]]", `forge.intake = "poll"`},
		},
		{
			name:       "both intake plus identities is rejected",
			intake:     config.ForgeIntakeBoth,
			identities: []config.IdentityConfig{identity},
			want:       []string{`forge.intake "both"`, "alongside [[identities]]", `forge.intake = "poll"`},
		},
		{
			name:       "poll intake plus identities stays valid",
			intake:     config.ForgeIntakePoll,
			identities: []config.IdentityConfig{identity},
		},
		{
			name:       "webhook intake with no identities stays valid",
			intake:     config.ForgeIntakeWebhook,
			identities: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := minimalValidConfig()
			cfg.Forge.Intake = tc.intake
			cfg.Forge.WebhookAddr = "0.0.0.0:8645"
			if tc.intake == config.ForgeIntakeWebhook || tc.intake == config.ForgeIntakeBoth {
				// Supply the secret and address the receiver would need, so a
				// rejection can only come from the combination itself and not
				// from a missing webhook setting.
				cfg.Forge.WebhookSecret = config.SecretRef{Engine: "env", Key: "ARCHIE_WEBHOOK_SECRET"}
			}
			cfg.Identities = tc.identities
			requireValidateError(t, validateForgeIntake(&cfg), tc.want)
		})
	}
}

// TestLoaderRejectsIdentityIntakeFromFile drives the rejection through the
// real file path, because the defect was that the key DECODES. Asserting on
// validateForgeIntake alone would not prove an operator's
// [identities.forge] intake = "webhook" is refused where they would hit it.
func TestLoaderRejectsIdentityIntakeFromFile(t *testing.T) {
	const doc = `
bot_user = "archie-bot"

[services.gateway]
target = "127.0.0.1:8585"

[forge]
type = "none"

[containers]
image = "ghcr.io/samcharles93/archie-agent:latest"

[[identities]]
name = "personal"
bot_user = "archie-personal"

[identities.forge]
type = "github"
intake = "webhook"
token = { engine = "env", key = "ARCHIE_GITHUB_TOKEN" }

[[identities.repos]]
owner = "my-org"
name = "my-repo"
`
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(nil).File(path)
	requireValidateError(t, err, []string{`identities[0] ("personal").forge.intake "webhook"`, "not implemented"})
}

// TestShippedProfilesValidate guards the shipped deployments against a
// validation rule that would reject a profile the repository supports:
// multi-forge-github-gitea.toml is the only profile using [[identities]], and
// single-forge-github.toml the only documented webhook-capable shape, so an
// intake rule that fails either has broken a supported assembly.
func TestShippedProfilesValidate(t *testing.T) {
	for _, name := range []string{"single-forge-github.toml", "multi-forge-github-gitea.toml"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("..", "..", "..", "deployments", name)
			doc, err := New(nil).File(path)
			if err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			if err := Validate(&doc.Config); err != nil {
				t.Fatalf("validate %s: %v", path, err)
			}
		})
	}
}

// requireValidateError asserts err wraps ErrInvalidInput and carries every
// fragment in want. An error that only said "invalid input" would leave the
// operator guessing which of the file's settings to fix, so every rejection
// above is asserted on the words that name the setting.
func requireValidateError(t *testing.T, err error, want []string) {
	t.Helper()
	if len(want) == 0 {
		if err != nil {
			t.Fatalf("got error %v, want the configuration accepted", err)
		}
		return
	}
	if err == nil {
		t.Fatal("got nil error, want a rejection")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error %v does not wrap ErrInvalidInput", err)
	}
	for _, fragment := range want {
		if !strings.Contains(err.Error(), fragment) {
			t.Errorf("error %q does not name %q", err, fragment)
		}
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
