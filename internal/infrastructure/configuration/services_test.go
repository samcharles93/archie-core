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
