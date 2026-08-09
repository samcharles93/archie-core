package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// writeConfig writes a TOML config file. The minimal shape (bot_user +
// one repo) is the same fixture loader_test.go uses to satisfy
// validation.
func writeConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func minimalConfigTOML(botUser string) string {
	return "bot_user = \"" + botUser + "\"\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
}

func invalidConfigTOML() string {
	return "bot_user = \"widget\"\nagent = { mode = \"bogus-mode\" }\n[[repos]]\nowner = \"acme\"\nname = \"app\"\n"
}

func TestReloadAppliesAndPublishes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeConfig(t, path, minimalConfigTOML("first"))

	loader := configuration.New(slog.New(slog.DiscardHandler))
	var published config.Config
	controller := newReloadController(loader, path, "", func(doc *configuration.Document) {
		published = doc.Config
	})

	if err := controller.Reload(); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	if published.BotUser != "first" {
		t.Fatalf("published BotUser = %q, want first", published.BotUser)
	}

	writeConfig(t, path, minimalConfigTOML("second"))
	if err := controller.Reload(); err != nil {
		t.Fatalf("second reload: %v", err)
	}
	if published.BotUser != "second" {
		t.Fatalf("published BotUser = %q, want second", published.BotUser)
	}
	if st := controller.Status(); st.LastReloadAt == "" || st.LastError != "" {
		t.Fatalf("Status after success = %+v, want LastReloadAt set and no error", st)
	}
}

// TestReloadValidationFailureDoesNotApply pins the safety property of
// the whole reload feature: a reload that fails validation must not
// publish. If apply were called on the error path, a bad file would
// half-apply or corrupt the running config.
func TestReloadValidationFailureDoesNotApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeConfig(t, path, minimalConfigTOML("widget"))

	loader := configuration.New(slog.New(slog.DiscardHandler))
	applied := 0
	controller := newReloadController(loader, path, "", func(*configuration.Document) { applied++ })

	if err := controller.Reload(); err != nil {
		t.Fatalf("first reload: %v", err)
	}
	if applied != 1 {
		t.Fatalf("apply called %d times after successful reload, want 1", applied)
	}

	writeConfig(t, path, invalidConfigTOML())
	err := controller.Reload()
	if err == nil {
		t.Fatal("reload with invalid config: expected an error")
	}
	if applied != 1 {
		t.Fatalf("apply called %d times after failed reload, want still 1", applied)
	}
	if st := controller.Status(); st.LastError == "" || st.LastErrorAt == "" {
		t.Fatalf("Status after failed reload = %+v, want LastError and LastErrorAt set", st)
	}

	// A subsequent valid reload clears the error.
	writeConfig(t, path, minimalConfigTOML("widget"))
	if err := controller.Reload(); err != nil {
		t.Fatalf("recovery reload: %v", err)
	}
	if st := controller.Status(); st.LastError != "" || st.LastReloadAt == "" {
		t.Fatalf("Status after recovery = %+v, want no error and a reload time", st)
	}
}

func TestReloadLoopAppliesOnSignalAndStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	writeConfig(t, path, minimalConfigTOML("widget"))

	loader := configuration.New(slog.New(slog.DiscardHandler))
	applied := make(chan struct{}, 1)
	controller := newReloadController(loader, path, "", func(*configuration.Document) {
		applied <- struct{}{}
	})

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		reloadLoop(ctx, ch, controller, slog.New(slog.DiscardHandler))
	})

	ch <- syscall.SIGHUP
	select {
	case <-applied:
	case <-time.After(5 * time.Second):
		t.Fatal("reload not applied after signal")
	}

	cancel()
	wg.Wait()
}

func TestChangedNonReloadableFields(t *testing.T) {
	base := config.Config{
		BotUser:      "a",
		MaxRetries:   3,
		PollInterval: config.Duration(30 * time.Second),
		Repos:        []config.Repo{{Owner: "x", Name: "y"}},
	}

	// Unchanged -> empty.
	if got := changedNonReloadableFields(base, base); len(got) != 0 {
		t.Fatalf("unchanged: got %v, want []", got)
	}

	// Reloadable-only changes (PollInterval, Repos, Budgets, Dispatch,
	// DiffCapLines) -> empty.
	reloadableOnly := base
	reloadableOnly.PollInterval = config.Duration(60 * time.Second)
	reloadableOnly.Repos = []config.Repo{{Owner: "x", Name: "z"}}
	reloadableOnly.Budgets = config.Budgets{MaxSteps: 99}
	reloadableOnly.Dispatch = config.Dispatch{Trigger: "label"}
	reloadableOnly.DiffCapLines = 1234
	if got := changedNonReloadableFields(base, reloadableOnly); len(got) != 0 {
		t.Fatalf("reloadable-only: got %v, want []", got)
	}

	// A non-reloadable change is named.
	nonReloadable := base
	nonReloadable.BotUser = "b"
	if got := changedNonReloadableFields(base, nonReloadable); len(got) != 1 || got[0] != "BotUser" {
		t.Fatalf("BotUser change: got %v, want [BotUser]", got)
	}

	// Containers sub-field granularity: Image change warns, MaxConcurrency
	// and VolumeTTL changes do not.
	c1 := base
	c1.Containers = config.ContainerConfig{Image: "img1", MaxConcurrency: 2, VolumeTTL: config.Duration(24 * time.Hour)}
	c2 := c1
	c2.Containers.Image = "img2"
	if got := changedNonReloadableFields(c1, c2); len(got) != 1 || got[0] != "Containers.Image" {
		t.Fatalf("Image change: got %v, want [Containers.Image]", got)
	}
	c3 := c1
	c3.Containers.MaxConcurrency = 4
	if got := changedNonReloadableFields(c1, c3); len(got) != 0 {
		t.Fatalf("MaxConcurrency change: got %v, want []", got)
	}
	c4 := c1
	c4.Containers.VolumeTTL = config.Duration(48 * time.Hour)
	if got := changedNonReloadableFields(c1, c4); len(got) != 0 {
		t.Fatalf("VolumeTTL change: got %v, want []", got)
	}

	// NATS is not reloadable: a URL change warns (the daemon's client is
	// startup-built; containers get the frozen ConnectedNATS).
	natsChanged := base
	natsChanged.NATS = config.NATSConfig{URL: "nats://new:4222"}
	if got := changedNonReloadableFields(base, natsChanged); len(got) != 1 || got[0] != "NATS" {
		t.Fatalf("NATS change: got %v, want [NATS]", got)
	}
}
