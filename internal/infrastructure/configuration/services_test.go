package configuration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGatewayServiceTarget(t *testing.T) {
	for _, tt := range []struct {
		name, section, wantTarget string
	}{
		{name: "default", wantTarget: "127.0.0.1:8585"},
		{name: "explicit target", section: "[services.gateway]\ntarget = 'dns:///gateway:8443'", wantTarget: "dns:///gateway:8443"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("bot_user = 'widget'\n"+tt.section), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Services.Gateway.Target != tt.wantTarget {
				t.Fatalf("target = %q, want %q", cfg.Services.Gateway.Target, tt.wantTarget)
			}
		})
	}
}

// TestLoadServiceListens: both gRPC services this repository runs bind an
// address the configuration names. They used to be flag-only, so a host that
// already owned the default port (cockpit owns 9090) could only be retargeted
// by passing -listen and hand-writing an overlay whose target matched -- two
// files to keep in step, and nothing to keep them there.
func TestLoadServiceListens(t *testing.T) {
	for _, tt := range []struct {
		name, section, wantState, wantGateway string
	}{
		{name: "defaults", wantState: "127.0.0.1:9090", wantGateway: "127.0.0.1:8585"},
		{
			name:        "explicit listens",
			section:     "[services.state]\nlisten = '127.0.0.1:9191'\n[services.gateway]\nlisten = '127.0.0.1:8686'",
			wantState:   "127.0.0.1:9191",
			wantGateway: "127.0.0.1:8686",
		},
		{
			name:        "state listen alone leaves the gateway default",
			section:     "[services.state]\nlisten = '127.0.0.1:9191'",
			wantState:   "127.0.0.1:9191",
			wantGateway: "127.0.0.1:8585",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("bot_user = 'widget'\n"+tt.section), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Services.State.Listen != tt.wantState {
				t.Fatalf("state listen = %q, want %q", cfg.Services.State.Listen, tt.wantState)
			}
			if cfg.Services.Gateway.Listen != tt.wantGateway {
				t.Fatalf("gateway listen = %q, want %q", cfg.Services.Gateway.Listen, tt.wantGateway)
			}
		})
	}
}

// TestLoadHealthListen: the daemon's liveness surface is not optional. The
// update watchdog restarts archied and asks whether it came back, and only
// archied can answer that -- the dashboard's /healthz belongs to the
// dashboard, which can be switched off and moves to its own process
// (archie-core-1r4g, archie-core-exbz).
func TestLoadHealthListen(t *testing.T) {
	for _, tt := range []struct {
		name, section, wantListen string
	}{
		{name: "default", wantListen: "127.0.0.1:8485"},
		{name: "explicit listen", section: "[health]\nlisten = '127.0.0.1:9500'", wantListen: "127.0.0.1:9500"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("bot_user = 'widget'\n"+tt.section), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Health.Listen != tt.wantListen {
				t.Fatalf("health listen = %q, want %q", cfg.Health.Listen, tt.wantListen)
			}
		})
	}
}
