package configuration

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGatewayServiceMode(t *testing.T) {
	for _, tt := range []struct {
		name, section string
		wantError     bool
		wantTarget    string
	}{
		{name: "default", wantTarget: "127.0.0.1:8585"},
		{name: "remote", section: "[services.gateway]\nmode = 'remote'\ntarget = 'dns:///gateway:8443'", wantTarget: "dns:///gateway:8443"},
		{name: "invalid mode", section: "[services.gateway]\nmode = 'typo'", wantError: true},
		{name: "inproc removed", section: "[services.gateway]\nmode = 'inproc'", wantError: true},
		{name: "remote without target", section: "[services.gateway]\nmode = 'remote'", wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte("bot_user = 'widget'\n"+tt.section), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := loadFile(path)
			if (err != nil) != tt.wantError {
				t.Fatalf("load error = %v, want error %v", err, tt.wantError)
			}
			if err == nil {
				if cfg.Services.Gateway.Mode != "remote" {
					t.Fatalf("mode = %q, want %q", cfg.Services.Gateway.Mode, "remote")
				}
				if cfg.Services.Gateway.Target != tt.wantTarget {
					t.Fatalf("target = %q, want %q", cfg.Services.Gateway.Target, tt.wantTarget)
				}
			}
		})
	}
}
