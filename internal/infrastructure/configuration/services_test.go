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
	}{
		{name: "default"},
		{name: "local", section: "[services.gateway]\nmode = 'inproc'"},
		{name: "remote", section: "[services.gateway]\nmode = 'remote'\ntarget = 'dns:///gateway:8443'"},
		{name: "invalid mode", section: "[services.gateway]\nmode = 'typo'", wantError: true},
		{name: "remote without target", section: "[services.gateway]\nmode = 'remote'", wantError: true},
		{name: "local with ignored target", section: "[services.gateway]\nmode = 'inproc'\ntarget = 'gateway:8443'", wantError: true},
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
				want := "inproc"
				if tt.name == "remote" {
					want = "remote"
					if cfg.Services.Gateway.Target != "dns:///gateway:8443" {
						t.Fatalf("target = %q", cfg.Services.Gateway.Target)
					}
				}
				if cfg.Services.Gateway.Mode != want {
					t.Fatalf("mode = %q, want %q", cfg.Services.Gateway.Mode, want)
				}
			}
		})
	}
}
