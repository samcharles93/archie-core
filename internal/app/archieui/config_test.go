package archieui

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/webui"
)

// uiConfigFile writes a config.toml carrying both the six fields the UI
// process is allowed to read and a spread of daemon-only settings it must
// ignore.
func uiConfigFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	contents := `bot_user = "widget"
work_dir = "/var/lib/archie/work"
skills_dir = "/var/lib/archie/skills"

[forge]
type = "none"
host = "https://forge.example.com"

[web]
listen = "127.0.0.1:9999"
trust_forwarded_headers = true

[log]
file = "/var/log/archie.log"

[services.gateway]
target = "127.0.0.1:8585"
target_token = "gateway-token"

[services.state]
target = "127.0.0.1:9090"
target_token = "state-token"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestResolveReadsOnlyTheAllowlistedConfigProjection pins the maintainer's
// decision for this bead: the UI process does read config.toml, but only
// Services.Gateway.{Target,TargetToken}, Services.State.{Target,TargetToken}
// and Web.{Listen,TrustForwardedHeaders}. Options has no field that could
// carry anything else, so this asserts the six values arrive and the daemon's
// own settings have nowhere to land.
func TestResolveReadsOnlyTheAllowlistedConfigProjection(t *testing.T) {
	resolved, err := Resolve(Options{Config: uiConfigFile(t)}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Listen != "127.0.0.1:9999" {
		t.Errorf("Listen = %q, want the file's web.listen", resolved.Listen)
	}
	if !resolved.trustForwardedHeaders() {
		t.Error("TrustForwardedHeaders = false, want the file's web.trust_forwarded_headers")
	}
	if resolved.Gateway != (ServiceTarget{Target: "127.0.0.1:8585", Token: "gateway-token"}) {
		t.Errorf("Gateway = %+v, want the file's [services.gateway]", resolved.Gateway)
	}
	if resolved.State != (ServiceTarget{Target: "127.0.0.1:9090", Token: "state-token"}) {
		t.Errorf("State = %+v, want the file's [services.state]", resolved.State)
	}
}

// TestResolveFlagsWinOverConfigFile is the other half of that decision:
// "Flags take precedence over file values."
func TestResolveFlagsWinOverConfigFile(t *testing.T) {
	trust := false
	resolved, err := Resolve(Options{
		Config:                uiConfigFile(t),
		Listen:                "127.0.0.1:7777",
		TrustForwardedHeaders: &trust,
		Gateway:               ServiceTarget{Target: "127.0.0.1:1111", Token: "flag-gateway"},
		State:                 ServiceTarget{Target: "127.0.0.1:2222", Token: "flag-state"},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Listen != "127.0.0.1:7777" {
		t.Errorf("Listen = %q, want the flag value", resolved.Listen)
	}
	if resolved.trustForwardedHeaders() {
		t.Error("TrustForwardedHeaders = true, want the explicit -trust-forwarded-headers=false to override the file")
	}
	if resolved.Gateway.Target != "127.0.0.1:1111" || resolved.Gateway.Token != "flag-gateway" {
		t.Errorf("Gateway = %+v, want the flag values", resolved.Gateway)
	}
	if resolved.State.Target != "127.0.0.1:2222" || resolved.State.Token != "flag-state" {
		t.Errorf("State = %+v, want the flag values", resolved.State)
	}
}

// TestResolveRunsWithoutAConfigFile keeps the process usable in a deployment
// that passes every endpoint as a flag: an absent config path is not an
// error, an unreadable one is.
func TestResolveRunsWithoutAConfigFile(t *testing.T) {
	resolved, err := Resolve(Options{
		Config:  filepath.Join(t.TempDir(), "absent.toml"),
		Gateway: ServiceTarget{Target: "127.0.0.1:8585"},
		State:   ServiceTarget{Target: "127.0.0.1:9090"},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve with an absent config file: %v", err)
	}
	if resolved.Listen != defaultListen {
		t.Errorf("Listen = %q, want the default %q", resolved.Listen, defaultListen)
	}
}

// TestResolveRequiresBothServiceTargets keeps the UI a client of two real
// services: neither contract has an in-process fallback in this process.
func TestResolveRequiresBothServiceTargets(t *testing.T) {
	tests := []struct {
		name    string
		options Options
	}{
		{name: "no gateway target", options: Options{State: ServiceTarget{Target: "127.0.0.1:9090"}}},
		{name: "no state target", options: Options{Gateway: ServiceTarget{Target: "127.0.0.1:8585"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Resolve(tt.options, slog.New(slog.DiscardHandler)); err == nil {
				t.Fatal("Resolve succeeded without both service targets")
			}
		})
	}
}

// TestResolveTokenFallsBackToEnv pins D1: the UI process reads the same
// [services.*].target_token keys the daemon does, but the config loader does
// not expand environment variables. A deployment that presents the gateway
// and state tokens by environment (config.example.toml documents this for
// remote consumers) must still authenticate, so an empty target_token falls
// back to GATEWAY_TOKEN / STATE_STORE_TOKEN. The daemon resolves the same two
// names through a secret.Registry, which also consults bws; this process reads
// the environment only, deliberately (see withEnvTokens).
func TestResolveTokenFallsBackToEnv(t *testing.T) {
	path := t.TempDir() + "/ui.toml"
	contents := `bot_user = "widget"

[web]
listen = "127.0.0.1:9999"

[services.gateway]
target = "127.0.0.1:8585"

[services.state]
target = "127.0.0.1:9090"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GATEWAY_TOKEN", "env-gateway")
	t.Setenv("STATE_STORE_TOKEN", "env-state")

	resolved, err := Resolve(Options{Config: path}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Gateway.Token != "env-gateway" {
		t.Errorf("Gateway.Token = %q, want env GATEWAY_TOKEN", resolved.Gateway.Token)
	}
	if resolved.State.Token != "env-state" {
		t.Errorf("State.Token = %q, want env STATE_STORE_TOKEN", resolved.State.Token)
	}

	// The explicit key still wins over the environment.
	keyed := `bot_user = "widget"

[services.gateway]
target = "127.0.0.1:8585"
target_token = "keyed-gateway"
[services.state]
target = "127.0.0.1:9090"
`
	path2 := t.TempDir() + "/keyed.toml"
	if err := os.WriteFile(path2, []byte(keyed), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved2, err := Resolve(Options{Config: path2}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve (keyed): %v", err)
	}
	if resolved2.Gateway.Token != "keyed-gateway" {
		t.Errorf("Gateway.Token = %q, want the explicit target_token over env", resolved2.Gateway.Token)
	}
}

// The activity feed is a poll in this process, so its interval is a
// process-local setting with a default, like the readiness timeout. Zero
// means "operator did not say", not "never poll".
func TestResolveDefaultsTheEventPollInterval(t *testing.T) {
	resolved, err := Resolve(Options{
		Gateway: ServiceTarget{Target: "127.0.0.1:8585"},
		State:   ServiceTarget{Target: "127.0.0.1:9090"},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.EventPollInterval != webui.DefaultEventPollInterval {
		t.Fatalf("EventPollInterval = %s, want the default %s", resolved.EventPollInterval, webui.DefaultEventPollInterval)
	}

	explicit, err := Resolve(Options{
		Gateway:           ServiceTarget{Target: "127.0.0.1:8585"},
		State:             ServiceTarget{Target: "127.0.0.1:9090"},
		EventPollInterval: 250 * time.Millisecond,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve with an explicit interval: %v", err)
	}
	if explicit.EventPollInterval != 250*time.Millisecond {
		t.Fatalf("EventPollInterval = %s, want the operator's 250ms", explicit.EventPollInterval)
	}
}

// The flags-only deployment is the one the env fallback exists for: a
// container or unit that passes both targets on the command line and presents
// the tokens by environment, with no config.toml on disk. readProjection
// returns early for an absent file, so a fallback that lives inside the
// file's projection never runs there.
func TestResolveTokenFallsBackToEnvWithoutAConfigFile(t *testing.T) {
	t.Setenv("GATEWAY_TOKEN", "env-gateway")
	t.Setenv("STATE_STORE_TOKEN", "env-state")

	resolved, err := Resolve(Options{
		Config:  filepath.Join(t.TempDir(), "absent.toml"),
		Listen:  "127.0.0.1:8484",
		Gateway: ServiceTarget{Target: "10.0.0.1:8585"},
		State:   ServiceTarget{Target: "10.0.0.1:9090"},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Gateway.Token != "env-gateway" {
		t.Errorf("Gateway.Token = %q, want env GATEWAY_TOKEN: staterpc.Dial refuses a non-loopback target without one", resolved.Gateway.Token)
	}
	if resolved.State.Token != "env-state" {
		t.Errorf("State.Token = %q, want env STATE_STORE_TOKEN", resolved.State.Token)
	}
}

// An explicit flag still beats the environment, so an operator can override a
// unit-wide token for one process without unsetting it.
func TestResolveFlagTokenBeatsEnv(t *testing.T) {
	t.Setenv("GATEWAY_TOKEN", "env-gateway")
	t.Setenv("STATE_STORE_TOKEN", "env-state")

	resolved, err := Resolve(Options{
		Config:  filepath.Join(t.TempDir(), "absent.toml"),
		Gateway: ServiceTarget{Target: "10.0.0.1:8585", Token: "flag-gateway"},
		State:   ServiceTarget{Target: "10.0.0.1:9090", Token: "flag-state"},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Gateway.Token != "flag-gateway" || resolved.State.Token != "flag-state" {
		t.Errorf("tokens = %q/%q, want the flag values to beat the environment", resolved.Gateway.Token, resolved.State.Token)
	}
}

// withDefaults' "off" coercion is the one key the UI and daemon read with
// opposite meanings, and its own comment asks for this pin.
func TestResolveCoercesListenOffToTheDefault(t *testing.T) {
	resolved, err := Resolve(Options{
		Config:  filepath.Join(t.TempDir(), "absent.toml"),
		Listen:  "off",
		Gateway: ServiceTarget{Target: "127.0.0.1:8585"},
		State:   ServiceTarget{Target: "127.0.0.1:9090"},
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Listen != defaultListen {
		t.Fatalf("Listen = %q, want %q: a dedicated UI process has nothing to disable", resolved.Listen, defaultListen)
	}
}
