package archied

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadConfigWarnsOnUnknownKeys pins plan-config-drift.md step 3: a
// stray/misspelled config key must be visible to the operator, not just
// silently parsed, validated and dropped.
func TestLoadConfigWarnsOnUnknownKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfig(t, path, minimalConfigTOML("widget")+"[containers]\nmax_concurrancy = 4\n")

	var sb strings.Builder
	log := slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b := &boot{log: log}

	if err := b.loadConfig(t.Context(), path, ""); err != nil {
		t.Fatalf("loadConfig: %v (an unknown key must not fail the boot)", err)
	}

	out := sb.String()
	if !strings.Contains(out, "containers.max_concurrancy") {
		t.Errorf("log does not name the unknown key: %s", out)
	}
}

// TestLoadConfigDoesNotWarnOnAKnownConfig is the negative case: a config
// with no unknown keys must produce no warning, so the new check cannot
// itself become log noise for a working deployment.
func TestLoadConfigDoesNotWarnOnAKnownConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfig(t, path, minimalConfigTOML("widget"))

	var sb strings.Builder
	log := slog.New(slog.NewTextHandler(&sb, &slog.HandlerOptions{Level: slog.LevelWarn}))
	b := &boot{log: log}

	if err := b.loadConfig(t.Context(), path, ""); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}

	if out := sb.String(); strings.Contains(out, "unrecognised") || strings.Contains(out, "unknown") {
		t.Errorf("unexpected warning for a config with no unknown keys: %s", out)
	}
}

func TestLoadConfigMakesRuntimeSettingsAvailableBeforeSubsystemSetup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	writeConfig(t, path, minimalConfigTOML("widget"))
	b := &boot{log: slog.Default()}

	if err := b.loadConfig(t.Context(), path, ""); err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if b.cfgHolder == nil {
		t.Fatal("runtime config holder is nil before control-plane settings load")
	}
}
