package archieui

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
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
