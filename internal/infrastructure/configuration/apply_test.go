package configuration

import (
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
)

func TestApplyOverlayValuesFieldLevelMerge(t *testing.T) {
	cfg := config.Config{
		BotUser: "a",
		Budgets: config.Budgets{MaxSteps: 10},
	}
	overrides := map[string]any{
		"budgets": map[string]any{"max_steps": float64(20)},
	}
	if err := ApplyOverlayValues(&cfg, overrides); err != nil {
		t.Fatal(err)
	}
	if cfg.Budgets.MaxSteps != 20 {
		t.Errorf("MaxSteps = %d, want 20", cfg.Budgets.MaxSteps)
	}
	// Fields the overlay omits keep their value.
	if cfg.BotUser != "a" {
		t.Errorf("BotUser = %q, want a (unchanged)", cfg.BotUser)
	}
}

// TestApplyOverlayValuesPreservesOmittedNestedFields pins ApplyOverlayValues'
// documented promise -- "only the keys present are replaced, every field the
// overlay omits keeps its existing value" -- at the depth where it matters.
//
// The concrete risk is services.state: an overlay that changes only the target
// must not clear target_token, because dropping the token silently removes
// authentication for a non-loopback State Store. The existing field-level test
// only covers one level (budgets.max_steps); these cases go two levels deep into
// a map of structs, which is the shape Services has.
func TestApplyOverlayValuesPreservesOmittedNestedFields(t *testing.T) {
	tests := []struct {
		name    string
		start   config.Services
		overlay map[string]any
		want    config.Services
	}{
		{
			name:    "target-only overlay keeps the token",
			start:   config.Services{config.ServiceNameState: {Target: "127.0.0.1:50051", TargetToken: "secret"}},
			overlay: map[string]any{"services": map[string]any{config.ServiceNameState: map[string]any{"target": "10.0.0.5:50051"}}},
			want:    config.Services{config.ServiceNameState: {Target: "10.0.0.5:50051", TargetToken: "secret"}},
		},
		{
			name:    "token-only overlay keeps the target",
			start:   config.Services{config.ServiceNameState: {Target: "10.0.0.5:50051", TargetToken: "old"}},
			overlay: map[string]any{"services": map[string]any{config.ServiceNameState: map[string]any{"target_token": "rotated"}}},
			want:    config.Services{config.ServiceNameState: {Target: "10.0.0.5:50051", TargetToken: "rotated"}},
		},
		{
			name:    "sibling service is untouched",
			start:   config.Services{config.ServiceNameGateway: {Target: "127.0.0.1:50052"}, config.ServiceNameState: {Target: "a", TargetToken: "t"}},
			overlay: map[string]any{"services": map[string]any{config.ServiceNameState: map[string]any{"target": "b"}}},
			want:    config.Services{config.ServiceNameGateway: {Target: "127.0.0.1:50052"}, config.ServiceNameState: {Target: "b", TargetToken: "t"}},
		},
		{
			name:    "an entry the overlay introduces decodes in full",
			start:   config.Services{},
			overlay: map[string]any{"services": map[string]any{config.ServiceNameState: map[string]any{"target": "127.0.0.1:50051", "target_token": "fresh"}}},
			want:    config.Services{config.ServiceNameState: {Target: "127.0.0.1:50051", TargetToken: "fresh"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{Services: tt.start}
			if err := ApplyOverlayValues(&cfg, tt.overlay); err != nil {
				t.Fatalf("ApplyOverlayValues: %v", err)
			}
			if !maps.Equal(cfg.Services, tt.want) {
				t.Errorf("Services = %+v, want %+v", cfg.Services, tt.want)
			}
		})
	}
}

// TestApplyOverlayValuesMergesEveryMapEntry pins that the preservation above is
// a property of ApplyOverlayValues rather than of Services. yaml.v3 decodes a
// map value into a fresh zero value, so every map[string]struct field loses the
// fields an override omits; Services, image.hosted and providers are the three
// the configuration has, so a fix that named one of them would leave the other
// two silently zeroing operator settings.
func TestApplyOverlayValuesMergesEveryMapEntry(t *testing.T) {
	tests := []struct {
		name    string
		start   config.Config
		overlay map[string]any
		check   func(t *testing.T, cfg config.Config)
	}{
		{
			name: "image.hosted entry keeps the fields the overlay omits",
			start: config.Config{Image: config.ImageConfig{Hosted: map[string]config.ImageHostedProvider{
				"minimax": {Enabled: true, Class: "minimax", APIKeyEnv: "MINIMAX_API_KEY"},
			}}},
			overlay: map[string]any{"image": map[string]any{"hosted": map[string]any{
				"minimax": map[string]any{"base_url": "https://api.example"},
			}}},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				want := map[string]config.ImageHostedProvider{
					"minimax": {Enabled: true, Class: "minimax", APIKeyEnv: "MINIMAX_API_KEY", BaseURL: "https://api.example"},
				}
				if !maps.Equal(cfg.Image.Hosted, want) {
					t.Errorf("Image.Hosted = %+v, want %+v", cfg.Image.Hosted, want)
				}
			},
		},
		{
			name: "providers entry keeps the fields the overlay omits",
			start: config.Config{Providers: map[string]config.Provider{
				"anthropic": {Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY"},
			}},
			overlay: map[string]any{"providers": map[string]any{
				"anthropic": map[string]any{"base_url": "https://proxy.example"},
			}},
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				want := map[string]config.Provider{
					"anthropic": {Class: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", BaseURL: "https://proxy.example"},
				}
				if !maps.Equal(cfg.Providers, want) {
					t.Errorf("Providers = %+v, want %+v", cfg.Providers, want)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.start
			if err := ApplyOverlayValues(&cfg, tt.overlay); err != nil {
				t.Fatalf("ApplyOverlayValues: %v", err)
			}
			tt.check(t, cfg)
		})
	}
}

func TestLoaderApplyOverlayLayersDefaultsValidatesAndRecordsProvenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bot_user = \"widget\"\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	l := New(slog.New(slog.DiscardHandler))
	doc, err := l.Resolve(path, "")
	if err != nil {
		t.Fatal(err)
	}

	applied, err := l.ApplyOverlay(doc, map[string]any{"poll_interval": "90s"})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Config.PollInterval != config.Duration(90*time.Second) {
		t.Errorf("PollInterval = %v, want 90s", applied.Config.PollInterval)
	}
	if len(applied.Provenance.Origins) != 2 {
		t.Errorf("Origins = %+v, want 2 (file + runtime overlay)", applied.Provenance.Origins)
	}

	// An overlay that fails validation aborts; ApplyOverlay never mutates
	// its input, so the base document is untouched even without a copy.
	if _, err := l.ApplyOverlay(doc, map[string]any{"dispatch": map[string]any{"trigger": "bogus"}}); err == nil {
		t.Fatal("invalid overlay accepted")
	}
	if doc.Config.Dispatch.Trigger != "assignee" {
		t.Errorf("base Dispatch.Trigger = %q, want assignee (defaulted, untouched)", doc.Config.Dispatch.Trigger)
	}
}

func TestLoaderApplyOverlayEmptyIsNoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("bot_user = \"widget\"\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := New(slog.New(slog.DiscardHandler))
	doc, err := l.Resolve(path, "")
	if err != nil {
		t.Fatal(err)
	}
	applied, err := l.ApplyOverlay(doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Provenance.Origins) != 1 {
		t.Errorf("Origins = %+v, want 1 (no overlay origin for an empty overlay)", applied.Provenance.Origins)
	}
}
