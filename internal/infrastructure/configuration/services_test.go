package configuration

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/samcharles93/archie-core/internal/config"
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
			if cfg.Services.Get(config.ServiceNameGateway).Target != tt.wantTarget {
				t.Fatalf("target = %q, want %q", cfg.Services.Get(config.ServiceNameGateway).Target, tt.wantTarget)
			}
		})
	}
}

// TestLoadServiceListens: both gRPC services this repository runs bind an
// address the configuration names. They used to be flag-only, so a host whose
// default port was already taken could only be retargeted
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
			if cfg.Services.Get(config.ServiceNameState).Listen != tt.wantState {
				t.Fatalf("state listen = %q, want %q", cfg.Services.Get(config.ServiceNameState).Listen, tt.wantState)
			}
			if cfg.Services.Get(config.ServiceNameGateway).Listen != tt.wantGateway {
				t.Fatalf("gateway listen = %q, want %q", cfg.Services.Get(config.ServiceNameGateway).Listen, tt.wantGateway)
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

// TestRegisteredServiceDefaultsNeedNoLoaderEdit pins criterion 4 of
// docs/prds/service-registry.md. A service registered anywhere gets its
// defaults applied here without this package naming it, which is the whole
// point of iterating the registry instead of writing one branch per service.
func TestRegisteredServiceDefaultsNeedNoLoaderEdit(t *testing.T) {
	config.RegisterService(config.ServiceBoth, "loadertest", "127.0.0.1:7101", "127.0.0.1:7102", "LOADERTEST_TOKEN")

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bot_user = 'widget'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Services.Get("loadertest")
	if got.Target != "127.0.0.1:7101" {
		t.Errorf("target = %q, want the registered default", got.Target)
	}
	if got.Listen != "127.0.0.1:7102" {
		t.Errorf("listen = %q, want the registered default", got.Listen)
	}
}

// TestStateTargetIsNotDefaulted keeps the asymmetry the registry now carries as
// data: the gateway's target is defaulted, the State Store's is operator
// input, and defaulting it to something dialable would turn a missing
// required value into a connection attempt against the wrong host.
func TestStateTargetIsNotDefaulted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bot_user = 'widget'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Services.Get(config.ServiceNameState).Target; got != "" {
		t.Errorf("state target = %q, want empty", got)
	}
}

// TestUnregisteredServiceSectionIsReported is the regression this change had to
// avoid introducing. Services was a struct, so [services.gatway] landed in
// Undecoded() and was reported. As a map it decodes cleanly, so without the
// registry check a typo'd service section would load silently and the daemon
// would dial the default instead.
func TestUnregisteredServiceSectionIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	src := minimalValidConfigTOML + "[services.gatway]\ntarget = '127.0.0.1:1'\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := New(nil).File(path)
	if err != nil {
		t.Fatalf("File: %v (an unknown key must not fail the load)", err)
	}
	if !slices.Contains(doc.UnknownKeys, "services.gatway") {
		t.Errorf("UnknownKeys = %v, want it to contain %q", doc.UnknownKeys, "services.gatway")
	}
}

// TestRegisteredServiceSectionIsNotReported is the other half: a real service
// section must not be flagged just because the map would accept anything.
func TestRegisteredServiceSectionIsNotReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	src := minimalValidConfigTOML + "[services.state]\ntarget = '127.0.0.1:9090'\n"
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := New(nil).File(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range doc.UnknownKeys {
		if strings.HasPrefix(k, "services.") {
			t.Errorf("UnknownKeys = %v, want no services.* entry for a registered service", doc.UnknownKeys)
			break
		}
	}
}
