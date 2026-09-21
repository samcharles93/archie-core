package archied

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// staleDatabaseOwnedSettings are TOML fragments, each carrying one value a
// control-plane resource is seeded from and replaces wholesale. They are the
// values docs/prds/runtime-control-plane.md,
// "Bootstrap, migration, and recovery" says must not block a process: "after
// migration, settings in TOML are ignored and cannot block State Store
// startup".
var staleDatabaseOwnedSettings = []struct {
	name string
	body string
}{
	{
		name: "provider base_url carrying userinfo",
		body: "[providers.openai]\nclass = \"openai\"\nbase_url = \"https://token@example.com/v1\"\n",
	},
	{
		name: "negative poll_interval",
		body: "poll_interval = \"-5s\"\n",
	},
	{
		name: "unrecognised dispatch.trigger",
		body: "[dispatch]\ntrigger = \"labels\"\n",
	},
	{
		name: "negative containers.volume_ttl",
		body: "[containers]\nvolume_ttl = \"-1m\"\n",
	},
	{
		name: "malformed repos test_glob",
		body: "[[repos]]\nowner = \"acme\"\nname = \"app\"\ntest_glob = \"[\"\n",
	},
}

// TestLoadConfigAcceptsStaleDatabaseOwnedValues is the first of the two layers
// archie-state-store boots through. loadConfig resolves the file and is where
// the loader used to reject these values; it must not, because the setting
// belongs to the control plane. The second layer is the boot itself, covered by
// TestStateStoreBootsWithStaleDatabaseOwnedValues -- passing only this one is
// not the item's claim.
func TestLoadConfigAcceptsStaleDatabaseOwnedValues(t *testing.T) {
	for _, tt := range staleDatabaseOwnedSettings {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir() + "/config.toml"
			writeConfig(t, path, "bot_user = \"widget\"\n"+tt.body)

			b := &boot{log: slog.New(slog.DiscardHandler)}
			if err := b.loadConfig(t.Context(), path, ""); err != nil {
				t.Fatalf("loadConfig: %v (a stale database-owned value must not fail boot)", err)
			}
		})
	}
}

// TestStateStoreBootsWithStaleDatabaseOwnedValues is the process-level half of
// the item, and the layer that actually decides it: archie-state-store seeds
// every control-plane resource from the same file config on a fresh database
// (state_store.go, control.ImportConfig), and that seed is what runs the
// resource's own validator. A seed that does not validate is skipped and
// reported, not fatal -- the value stays file-owned, archied refuses to run with
// it, and the operator's fix in config.toml is what clears it.
// staleBootGrace is how long one boot in
// TestStateStoreBootsWithStaleDatabaseOwnedValues may run before the test
// cancels it. It can be short because the assertion does not depend on it: a
// refusal arrives as an error either before the cancel (first select) or after
// it (second select, where a non-Canceled error fails the test). The wait only
// gives the serving path a chance to prove it did not return immediately.
const staleBootGrace = 250 * time.Millisecond

func TestStateStoreBootsWithStaleDatabaseOwnedValues(t *testing.T) {
	for _, tt := range staleDatabaseOwnedSettings {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			body := fmt.Sprintf(
				"bot_user = 'archie'\ndb_path = %q\n%s[forge]\ntype = 'github'\nhost = 'https://github.example.com'\n",
				filepath.Join(dir, "state.db"), tt.body,
			)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}

			// Loopback, so no token is required. Cancellation is the assertion:
			// a boot that refuses the config returns before it, and one that
			// serves returns only when the context ends. Cancel first, then
			// assert -- racing startup against a wall clock is a coin flip.
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			errCh := make(chan error, 1)
			go func() {
				errCh <- RunStateStore(ctx, StateStoreOptions{Config: path, Listen: "127.0.0.1:0"})
			}()

			select {
			case err := <-errCh:
				t.Fatalf("RunStateStore returned before cancellation, want it to serve: %v", err)
			case <-time.After(staleBootGrace):
			}
			cancel()
			select {
			case err := <-errCh:
				if err != nil && !errors.Is(err, context.Canceled) {
					t.Fatalf("RunStateStore returned %v, want nil or context.Canceled", err)
				}
			case <-time.After(shutdownGrace):
				t.Fatal("RunStateStore did not return within the shutdown grace after cancellation")
			}
		})
	}
}

// TestRuntimeConfigFailsClosedOnAnInvalidDatabaseValue is the other half of the
// same change: the checks that no longer run at load run on the effective
// document, at the one place database resources are layered in. A stored value
// that will not validate must stop archied starting rather than be published as
// the running configuration -- a state nothing can recover from in band.
func TestRuntimeConfigFailsClosedOnAnInvalidDatabaseValue(t *testing.T) {
	resources := databaseOwnedResources()
	resources[controlplane.ProviderSettingsKind] = map[string]any{
		"main": map[string]any{"class": "openai", "base_url": "https://token@example.com/v1"},
	}
	b := newReloadBoot(t, &controlPlaneStub{values: resources})

	err := b.loadRuntimeConfig(t.Context())
	if err == nil {
		t.Fatal("loadRuntimeConfig accepted a database value that does not validate")
	}
	if !errors.Is(err, configuration.ErrInvalidInput) {
		t.Errorf("loadRuntimeConfig = %v, want it to wrap configuration.ErrInvalidInput", err)
	}
	if got := b.cfgHolder.Get().Providers["main"].BaseURL; got == "https://token@example.com/v1" {
		t.Error("the invalid database value was published as the running configuration")
	}
}
