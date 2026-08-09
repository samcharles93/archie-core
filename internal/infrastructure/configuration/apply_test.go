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
	// Unrelated nested fields keep their value too.
	if cfg.Budgets.MaxTokens != 0 {
		t.Errorf("MaxTokens = %d, want 0 (untouched)", cfg.Budgets.MaxTokens)
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

	// An overlay that fails validation aborts; the base document is
	// untouched when a copy is passed.
	next := *doc
	if _, err := l.ApplyOverlay(&next, map[string]any{"agent": map[string]any{"mode": "bogus"}}); err == nil {
		t.Fatal("invalid overlay accepted")
	}
	if doc.Config.Agent.Mode != "inprocess" {
		t.Errorf("base Agent.Mode = %q, want inprocess (defaulted, untouched)", doc.Config.Agent.Mode)
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
