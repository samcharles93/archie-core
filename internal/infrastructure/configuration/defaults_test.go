package configuration

import (
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestApplyNATSDefaultsResolvesMode(t *testing.T) {
	tests := []struct {
		name string
		mode string
		url  string
		want string
	}{
		{name: "url set implies external", url: "nats://localhost:4222", want: config.NATSModeExternal},
		{name: "empty implies embedded", want: config.NATSModeEmbedded},
		{name: "explicit embedded is preserved", mode: config.NATSModeEmbedded, want: config.NATSModeEmbedded},
		{name: "explicit external is preserved", mode: config.NATSModeExternal, url: "nats://localhost:4222", want: config.NATSModeExternal},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{NATS: config.NATSConfig{Mode: tc.mode, URL: tc.url}}
			applyNATSDefaults(cfg)
			if cfg.NATS.Mode != tc.want {
				t.Errorf("applyNATSDefaults() mode = %q, want %q", cfg.NATS.Mode, tc.want)
			}
		})
	}
}

func TestApplyContainerDefaultsMakesManagedWorkersReadyByDefault(t *testing.T) {
	cfg := &config.Config{}
	applyContainerDefaults(cfg)

	if cfg.Containers.Image != defaultContainerImage {
		t.Errorf("Containers.Image = %q, want %q", cfg.Containers.Image, defaultContainerImage)
	}
	if cfg.Containers.MaxUptime.Std() != defaultContainerUptime {
		t.Errorf("Containers.MaxUptime = %s, want %s", cfg.Containers.MaxUptime.Std(), defaultContainerUptime)
	}
	if cfg.Containers.VolumeTTL.Std() != defaultVolumeTTL {
		t.Errorf("Containers.VolumeTTL = %s, want %s", cfg.Containers.VolumeTTL.Std(), defaultVolumeTTL)
	}
	if cfg.Containers.PullPolicy != defaultPullPolicy {
		t.Errorf("Containers.PullPolicy = %q, want %q", cfg.Containers.PullPolicy, defaultPullPolicy)
	}
}

func TestApplyForgeDefaultsIntake(t *testing.T) {
	cfg := &config.Config{}
	applyForgeDefaults(cfg)
	if cfg.Forge.Intake != config.ForgeIntakePoll {
		t.Errorf("applyForgeDefaults() intake = %q, want %q", cfg.Forge.Intake, config.ForgeIntakePoll)
	}

	explicit := &config.Config{Forge: config.Forge{Intake: config.ForgeIntakeWebhook}}
	applyForgeDefaults(explicit)
	if explicit.Forge.Intake != config.ForgeIntakeWebhook {
		t.Errorf("applyForgeDefaults() intake = %q, want unchanged %q", explicit.Forge.Intake, config.ForgeIntakeWebhook)
	}
}

func TestApplyIdentityDefaultsDerivesEmailPerIdentity(t *testing.T) {
	cfg := &config.Config{Identities: []config.IdentityConfig{
		{BotUser: "github-bot", Forge: config.Forge{Type: forgeTypeGitHub}},
		{BotUser: "gitea-bot", Forge: config.Forge{Type: forgeTypeGitea}},
		{BotUser: "explicit", BotEmail: "bot@example.test", Forge: config.Forge{Type: forgeTypeGitHub}},
	}}

	applyIdentityDefaults(cfg)

	want := []string{"github-bot@users.noreply.github.com", "gitea-bot@gitea.local", "bot@example.test"}
	for i := range want {
		if cfg.Identities[i].BotEmail != want[i] {
			t.Errorf("identity %d BotEmail = %q, want %q", i, cfg.Identities[i].BotEmail, want[i])
		}
	}
}

func TestApplyCaptureDefaultsFillsZeroValues(t *testing.T) {
	cfg := &config.Config{}
	applyCaptureDefaults(cfg)

	if got, want := cfg.Capture.Retention.Std(), defaultCaptureRetention; got != want {
		t.Errorf("Capture.Retention = %v, want %v", got, want)
	}
	if cfg.Capture.MaxEvents != defaultCaptureMaxEvents {
		t.Errorf("Capture.MaxEvents = %d, want %d", cfg.Capture.MaxEvents, defaultCaptureMaxEvents)
	}
	if cfg.Capture.MaxBodyBytes != defaultCaptureMaxBodyBytes {
		t.Errorf("Capture.MaxBodyBytes = %d, want %d", cfg.Capture.MaxBodyBytes, defaultCaptureMaxBodyBytes)
	}
	if cfg.Capture.RatePerSecond != defaultCaptureRatePerSecond {
		t.Errorf("Capture.RatePerSecond = %v, want %v", cfg.Capture.RatePerSecond, defaultCaptureRatePerSecond)
	}
	if cfg.Capture.RateBurst != defaultCaptureRateBurst {
		t.Errorf("Capture.RateBurst = %d, want %d", cfg.Capture.RateBurst, defaultCaptureRateBurst)
	}
}

func TestApplyCaptureDefaultsLeavesExplicitValuesAlone(t *testing.T) {
	cfg := &config.Config{
		Capture: config.CaptureConfig{
			Retention:     config.Duration(24 * time.Hour),
			MaxEvents:     42,
			MaxBodyBytes:  1024,
			RatePerSecond: 2.5,
			RateBurst:     9,
		},
	}
	applyCaptureDefaults(cfg)

	if cfg.Capture.Retention.Std() != 24*time.Hour {
		t.Errorf("Capture.Retention = %v, want unchanged 24h", cfg.Capture.Retention.Std())
	}
	if cfg.Capture.MaxEvents != 42 {
		t.Errorf("Capture.MaxEvents = %d, want unchanged 42", cfg.Capture.MaxEvents)
	}
	if cfg.Capture.MaxBodyBytes != 1024 {
		t.Errorf("Capture.MaxBodyBytes = %d, want unchanged 1024", cfg.Capture.MaxBodyBytes)
	}
	if cfg.Capture.RatePerSecond != 2.5 {
		t.Errorf("Capture.RatePerSecond = %v, want unchanged 2.5", cfg.Capture.RatePerSecond)
	}
	if cfg.Capture.RateBurst != 9 {
		t.Errorf("Capture.RateBurst = %d, want unchanged 9", cfg.Capture.RateBurst)
	}
}

func TestApplyMemoryDefaultsFillsAbsentEngineWithBuiltin(t *testing.T) {
	cfg := &config.Config{}
	applyMemoryDefaults(cfg)

	if cfg.Memory.Engine != memoryEngineBuiltin {
		t.Errorf("Memory.Engine = %q, want %q", cfg.Memory.Engine, memoryEngineBuiltin)
	}
}

func TestApplyMemoryDefaultsLeavesExplicitEngineAlone(t *testing.T) {
	cfg := &config.Config{Memory: config.MemoryConfig{Engine: "honcho"}}
	applyMemoryDefaults(cfg)

	if cfg.Memory.Engine != "honcho" {
		t.Errorf("Memory.Engine = %q, want unchanged %q", cfg.Memory.Engine, "honcho")
	}
}

// TestDefaultsKeepAnExplicitUnlimitedDiffCap is the regression case for
// diff_cap_lines = 0 silently becoming 400. The dashboard schema, and
// StageDiffCap itself, both document 0 as "no cap", but the defaults pass could
// not tell an explicit 0 from an absent key and rewrote both to 400 -- so the
// documented way to switch the cap off was unreachable through config.
func TestDefaultsKeepAnExplicitUnlimitedDiffCap(t *testing.T) {
	unlimited := 0
	cfg := config.Config{DiffCapLines: &unlimited}
	(&Loader{}).applyDefaults(&cfg)

	if cfg.DiffCapLines == nil {
		t.Fatal("DiffCapLines = nil, want the explicit 0 preserved")
	}
	if *cfg.DiffCapLines != 0 {
		t.Fatalf("DiffCapLines = %d, want 0: an explicit 0 means no cap", *cfg.DiffCapLines)
	}
	if cfg.DiffCap() != 0 {
		t.Fatalf("DiffCap() = %d, want 0 (unlimited)", cfg.DiffCap())
	}
}

// TestDefaultsApplyTheDiffCapWhenUnset pins the other half: an absent key still
// gets the safety cap, so omitting it never silently auto-opens huge PRs.
func TestDefaultsApplyTheDiffCapWhenUnset(t *testing.T) {
	cfg := config.Config{}
	(&Loader{}).applyDefaults(&cfg)

	if cfg.DiffCapLines == nil || *cfg.DiffCapLines != defaultDiffCapLines {
		t.Fatalf("DiffCapLines = %v, want %d when the key is absent", cfg.DiffCapLines, defaultDiffCapLines)
	}
}

// TestDefaultsFillTheHealthDependencyTimeout pins that the probe timeout has a
// default, so a config that never mentions it still bounds the Gateway call
// rather than letting a hung dependency stall the whole health report.
func TestDefaultsFillTheHealthDependencyTimeout(t *testing.T) {
	cfg := config.Config{}
	(&Loader{}).applyDefaults(&cfg)

	if got := time.Duration(cfg.Health.DependencyTimeout); got != defaultHealthDependencyTimeout {
		t.Fatalf("Health.DependencyTimeout = %v, want %v", got, defaultHealthDependencyTimeout)
	}
}

// TestDefaultsKeepAConfiguredHealthDependencyTimeout pins that an operator's
// value is not overwritten, which is the whole point of making it configurable.
func TestDefaultsKeepAConfiguredHealthDependencyTimeout(t *testing.T) {
	cfg := config.Config{Health: config.Health{DependencyTimeout: config.Duration(30 * time.Second)}}
	(&Loader{}).applyDefaults(&cfg)

	if got := time.Duration(cfg.Health.DependencyTimeout); got != 30*time.Second {
		t.Fatalf("Health.DependencyTimeout = %v, want the configured 30s", got)
	}
}
