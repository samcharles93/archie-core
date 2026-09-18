package configuration

import (
	"log/slog"
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
// a struct of structs, which is the shape Services has.
func TestApplyOverlayValuesPreservesOmittedNestedFields(t *testing.T) {
	tests := []struct {
		name    string
		start   config.Services
		overlay map[string]any
		want    config.Services
	}{
		{
			name:    "target-only overlay keeps the token",
			start:   config.Services{State: config.ServiceConnection{Target: "127.0.0.1:50051", TargetToken: "secret"}},
			overlay: map[string]any{"services": map[string]any{"state": map[string]any{"target": "10.0.0.5:50051"}}},
			want:    config.Services{State: config.ServiceConnection{Target: "10.0.0.5:50051", TargetToken: "secret"}},
		},
		{
			name:    "token-only overlay keeps the target",
			start:   config.Services{State: config.ServiceConnection{Target: "10.0.0.5:50051", TargetToken: "old"}},
			overlay: map[string]any{"services": map[string]any{"state": map[string]any{"target_token": "rotated"}}},
			want:    config.Services{State: config.ServiceConnection{Target: "10.0.0.5:50051", TargetToken: "rotated"}},
		},
		{
			name:    "sibling service is untouched",
			start:   config.Services{Gateway: config.ServiceConnection{Target: "127.0.0.1:50052"}, State: config.ServiceConnection{Target: "a", TargetToken: "t"}},
			overlay: map[string]any{"services": map[string]any{"state": map[string]any{"target": "b"}}},
			want:    config.Services{Gateway: config.ServiceConnection{Target: "127.0.0.1:50052"}, State: config.ServiceConnection{Target: "b", TargetToken: "t"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{Services: tt.start}
			if err := ApplyOverlayValues(&cfg, tt.overlay); err != nil {
				t.Fatalf("ApplyOverlayValues: %v", err)
			}
			if cfg.Services != tt.want {
				t.Errorf("Services = %+v, want %+v", cfg.Services, tt.want)
			}
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
