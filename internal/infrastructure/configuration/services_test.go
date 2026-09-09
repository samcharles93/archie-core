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
