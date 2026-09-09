package archied

import (
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/daemon"
	"github.com/samcharles93/archie-core/internal/installtype"
	"github.com/samcharles93/archie-core/internal/releaseupdate"
	"github.com/samcharles93/archie-core/internal/secret"
)

type telegramTestSecretEngine struct{}

func (telegramTestSecretEngine) Name() string    { return "bws" }
func (telegramTestSecretEngine) Version() string { return "test" }
func (telegramTestSecretEngine) Resolve(key string) (string, error) {
	if key == "telegram-token" {
		return "token-from-bws", nil
	}
	return "", nil
}

func TestResolveTelegramTokenPrefersSecretRef(t *testing.T) {
	registry := secret.NewRegistry()
	registry.Register(telegramTestSecretEngine{})
	cfg := config.TelegramConfig{
		Token:    secret.SecretRef{Engine: "bws", Key: "telegram-token"},
		TokenEnv: "TELEGRAM_LEGACY_TOKEN",
	}
	t.Setenv("TELEGRAM_LEGACY_TOKEN", "token-from-env")

	got, err := resolveTelegramToken(cfg, registry)
	if err != nil {
		t.Fatalf("resolveTelegramToken returned error: %v", err)
	}
	if got != "token-from-bws" {
		t.Fatalf("resolveTelegramToken = %q, want token-from-bws", got)
	}
}

func TestResolveTelegramTokenFallsBackToTokenEnv(t *testing.T) {
	registry := secret.NewRegistry()
	cfg := config.TelegramConfig{TokenEnv: "TELEGRAM_LEGACY_TOKEN"}
	t.Setenv("TELEGRAM_LEGACY_TOKEN", "token-from-env")

	got, err := resolveTelegramToken(cfg, registry)
	if err != nil {
		t.Fatalf("resolveTelegramToken returned error: %v", err)
	}
	if got != "token-from-env" {
		t.Fatalf("resolveTelegramToken = %q, want token-from-env", got)
	}
}

func TestResolveTelegramTokenDoesNotUseTokenEnvWhenSecretRefIsSet(t *testing.T) {
	registry := secret.NewRegistry()
	registry.Register(telegramTestSecretEngine{})
	cfg := config.TelegramConfig{
		Token:    secret.SecretRef{Engine: "bws", Key: "telegram-token"},
		TokenEnv: "TELEGRAM_LEGACY_TOKEN",
	}
	t.Setenv("TELEGRAM_LEGACY_TOKEN", "token-from-env")

	got, err := resolveTelegramToken(cfg, registry)
	if err != nil {
		t.Fatalf("resolveTelegramToken returned error: %v", err)
	}
	if got == "token-from-env" {
		t.Fatal("resolveTelegramToken used TokenEnv despite Token being configured")
	}
}

// TestMakeUpdateServiceWiresInstallType is the regression test for
// archie-core-522's fail-closed guarantee actually reaching production: a
// Service built without InstallType always refuses to install (see
// releaseupdate.ErrUnknownInstallType), so wiring it here is what makes
// /update work at all for a correctly-stamped build, not just a detail.
func TestMakeUpdateServiceWiresInstallType(t *testing.T) {
	cfg := config.Config{}
	cfg.Chat.Telegram.UpdateCheckCommand = []string{"archie-update-check"}
	cfg.Chat.Telegram.UpdateInstallCommand = []string{"archie-update-install"}

	service := makeUpdateService(telegramSetup{Cfg: config.NewHolder(cfg)})

	if service == nil {
		t.Fatal("makeUpdateService returned nil despite a configured check command")
	}
	if service.InstallType != installtype.Type() {
		t.Errorf("Service.InstallType = %q, want installtype.Type() = %q", service.InstallType, installtype.Type())
	}
	if service.Enrich == nil {
		t.Fatal("Service.Enrich is nil; component InstallType/Reference will never be populated")
	}
}

func TestComponentInstallTypeEnricherDaemon(t *testing.T) {
	enrich := componentInstallTypeEnricher(nil, config.NATSConfig{})

	gotType, gotRef := enrich(releaseupdate.ComponentDaemon)
	if gotType != installtype.Type() || gotRef != "" {
		t.Errorf("daemon = (%q, %q), want (%q, \"\")", gotType, gotRef, installtype.Type())
	}
}

func TestComponentInstallTypeEnricherAgentBeforeAnyObservation(t *testing.T) {
	var status daemon.AgentStatus
	enrich := componentInstallTypeEnricher(&status, config.NATSConfig{})

	gotType, gotRef := enrich(releaseupdate.ComponentAgent)
	if gotType != "" || gotRef != "" {
		t.Errorf("agent before any Observe = (%q, %q), want (\"\", \"\")", gotType, gotRef)
	}
}

func TestComponentInstallTypeEnricherAgentAfterObservation(t *testing.T) {
	var status daemon.AgentStatus
	status.Observe("1.9.11", "container")
	enrich := componentInstallTypeEnricher(&status, config.NATSConfig{})

	gotType, _ := enrich(releaseupdate.ComponentAgent)
	if gotType != "container" {
		t.Errorf("agent InstallType = %q, want the observed install type %q", gotType, "container")
	}
}

func TestComponentInstallTypeEnricherAgentNilStatus(t *testing.T) {
	enrich := componentInstallTypeEnricher(nil, config.NATSConfig{})

	gotType, gotRef := enrich(releaseupdate.ComponentAgent)
	if gotType != "" || gotRef != "" {
		t.Errorf("agent with nil AgentStatus = (%q, %q), want (\"\", \"\")", gotType, gotRef)
	}
}

func TestComponentInstallTypeEnricherNATS(t *testing.T) {
	tests := []struct {
		name     string
		nats     config.NATSConfig
		wantType string
		wantRef  string
	}{
		{name: "embedded", nats: config.NATSConfig{Mode: "embedded"}, wantType: "embedded", wantRef: ""},
		{name: "external", nats: config.NATSConfig{Mode: "external", URL: "nats://nats.internal:4222"}, wantType: "external", wantRef: "nats://nats.internal:4222"},
		{name: "off", nats: config.NATSConfig{Mode: "off"}, wantType: "", wantRef: ""},
		{name: "unset", nats: config.NATSConfig{}, wantType: "", wantRef: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			enrich := componentInstallTypeEnricher(nil, test.nats)
			gotType, gotRef := enrich(releaseupdate.ComponentNATS)
			if gotType != test.wantType || gotRef != test.wantRef {
				t.Errorf("nats(%+v) = (%q, %q), want (%q, %q)", test.nats, gotType, gotRef, test.wantType, test.wantRef)
			}
		})
	}
}

func TestComponentInstallTypeEnricherUnknownComponent(t *testing.T) {
	enrich := componentInstallTypeEnricher(nil, config.NATSConfig{})

	gotType, gotRef := enrich("something-else")
	if gotType != "" || gotRef != "" {
		t.Errorf("unknown component = (%q, %q), want (\"\", \"\")", gotType, gotRef)
	}
}

// TestDaemonRunningVersionsVouchesOnlyForItselfWithoutAgentStatus: the point
// of checking a pending update report against running versions is that the
// value checked against is ground truth. This process's own compiled-in
// version is; a guess at the agent's is not, because runtimeVersion records
// what archied's release pipeline stamped, not what an agent container is
// actually running. Before any task has reported an observed agent version
// (agentStatus nil, or nothing observed yet), including one would recreate
// the false success the check exists to catch, so its absence is the
// assertion.
func TestDaemonRunningVersionsVouchesOnlyForItselfWithoutAgentStatus(t *testing.T) {
	running := daemonRunningVersions(nil)

	if got := running[releaseupdate.ComponentDaemon]; got != gatewayVersion {
		t.Errorf("running[%q] = %q, want gatewayVersion = %q", releaseupdate.ComponentDaemon, got, gatewayVersion)
	}
	if _, ok := running[releaseupdate.ComponentAgent]; ok {
		t.Errorf("running vouches for %q, which this process cannot verify: %#v",
			releaseupdate.ComponentAgent, running)
	}
}

// TestDaemonRunningVersionsIncludesObservedAgent: once a task response has
// actually self-reported an agent version (daemon.AgentStatus.Observe), that
// -- not archied's release pipeline's expectation -- is ground truth for
// what is running, so it belongs in the verification set.
func TestDaemonRunningVersionsIncludesObservedAgent(t *testing.T) {
	var status daemon.AgentStatus
	status.Observe("1.9.11", "container")

	running := daemonRunningVersions(&status)

	if got := running[releaseupdate.ComponentAgent]; got != "1.9.11" {
		t.Errorf("running[%q] = %q, want the observed version %q", releaseupdate.ComponentAgent, got, "1.9.11")
	}
}

// TestMakeUpdateServiceProbesTheDaemonsOwnHealthAddress: the watchdog is
// verifying archied's restart, so it has to probe archied. The dashboard's
// listener is the wrong answer twice over -- it can be switched off, and in
// Phase 3 it belongs to another process, which would report health for
// something the watchdog never restarted (archie-core-1r4g).
func TestMakeUpdateServiceProbesTheDaemonsOwnHealthAddress(t *testing.T) {
	for _, tc := range []struct{ name, listen, want string }{
		{name: "default loopback", listen: "127.0.0.1:8485", want: "http://127.0.0.1:8485"},
		{name: "wildcard bind", listen: "0.0.0.0:8485", want: "http://localhost:8485"},
		{name: "operator-chosen port", listen: "127.0.0.1:9500", want: "http://127.0.0.1:9500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{}
			cfg.Health.Listen = tc.listen
			// The dashboard's own address must not leak into the probe.
			cfg.Web.Listen = "127.0.0.1:8484"
			cfg.Chat.Telegram.UpdateCheckCommand = []string{"archie-update-check"}
			cfg.Chat.Telegram.UpdateInstallCommand = []string{"archie-update-install"}

			service := makeUpdateService(telegramSetup{Cfg: config.NewHolder(cfg)})

			if service == nil {
				t.Fatal("makeUpdateService returned nil despite a configured check command")
			}
			installer, ok := service.Installer.(releaseupdate.CommandInstaller)
			if !ok {
				t.Fatalf("Service.Installer = %T, want releaseupdate.CommandInstaller", service.Installer)
			}
			if installer.HealthURL != tc.want {
				t.Errorf("Installer.HealthURL = %q, want %q", installer.HealthURL, tc.want)
			}
		})
	}
}
