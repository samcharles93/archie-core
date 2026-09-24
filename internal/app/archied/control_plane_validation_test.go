package archied

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/config"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
	"github.com/samcharles93/archie-core/internal/infrastructure/postgres/pgtest"
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

// staleBootGrace is how long one boot in
// TestStateStoreBootsWithStaleDatabaseOwnedValues may run before the test cancels
// it. A refusal arrives as an error before the cancel (first select); a boot
// that serves returns only after the cancel (second select). The bound must be
// long enough for a full boot -- now Postgres pool + migration, then SQLite and
// the control-plane seed -- to finish, because cancelling mid-seed surfaces a
// transaction error that reads as a refusal rather than as the interrupt it is.
// 2s matches startupGrace, the sibling single-boot bound.
//
// staleShutdownGrace bounds the shutdown, and is deliberately not the 5s the
// single-boot regression test uses: this table runs five boots back to back on a
// host that may be busy, and the select returns the moment the process does, so
// the bound costs nothing when shutdown is quick and only decides how long a
// failure takes to report.
const (
	staleBootGrace     = 2 * time.Second
	staleShutdownGrace = 30 * time.Second
)

// TestStateStoreBootsWithStaleDatabaseOwnedValues is the process-level half of
// the item, and the layer that actually decides it: archie-state-store seeds
// every control-plane resource from the same file config on a fresh database
// (state_store.go, control.ImportConfig), and that seed is what runs the
// resource's own validator. A seed that does not validate is skipped and
// reported, not fatal -- the kind stays absent, the file's value is the one in
// effect, and the operator's fix in config.toml is what clears it.
func TestStateStoreBootsWithStaleDatabaseOwnedValues(t *testing.T) {
	t.Parallel()
	for _, tt := range staleDatabaseOwnedSettings {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "config.toml")
			body := fmt.Sprintf(
				"bot_user = 'archie'\ndatabase_url = %q\n%s[forge]\ntype = 'github'\nhost = 'https://github.example.com'\n",
				pgtest.URL(t), tt.body,
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
			case <-time.After(staleShutdownGrace):
				t.Fatal("RunStateStore did not return within the shutdown grace after cancellation")
			}
		})
	}
}

// TestRuntimeConfigKeepsTheFileValueWhenAKindHasNoStoredResource is the other
// end of a seed the control plane refused: the kind stays ABSENT rather than
// being stored (ImportConfig skips it), and boot must then fall back to the
// file document's value. Treating an absent kind as fatal is how a config the
// file layer blesses left the daemon unbootable with a database-named error,
// and left editing config.toml unable to fix it.
func TestRuntimeConfigKeepsTheFileValueWhenAKindHasNoStoredResource(t *testing.T) {
	tests := []struct {
		name  string
		kind  string
		check func(t *testing.T, cfg config.Config)
	}{
		{
			name: "repository policies",
			kind: controlplane.RepositoryPoliciesKind,
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if len(cfg.Repos) != 1 || cfg.Repos[0].Name != "from-file" {
					t.Errorf("Repos = %+v, want the file's from-file", cfg.Repos)
				}
			},
		},
		{
			name: "scheduling policy",
			kind: controlplane.SchedulingPolicyKind,
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.PollInterval != config.Duration(time.Minute) {
					t.Errorf("PollInterval = %v, want the file's 1m", cfg.PollInterval)
				}
			},
		},
		{
			name: "container runtime policies",
			kind: controlplane.ContainerRuntimePoliciesKind,
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if cfg.Containers.Image != "archie:from-file" {
					t.Errorf("Containers.Image = %q, want the file's archie:from-file", cfg.Containers.Image)
				}
			},
		},
		{
			name: "providers",
			kind: controlplane.ProviderSettingsKind,
			check: func(t *testing.T, cfg config.Config) {
				t.Helper()
				if got := cfg.Providers["file"].Class; got != "openai" {
					t.Errorf("Providers[file].Class = %q, want the file's openai", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := databaseOwnedResources()
			delete(resources, tt.kind)
			b := newReloadBoot(t, &controlPlaneStub{values: resources})

			if err := b.loadRuntimeConfig(t.Context()); err != nil {
				t.Fatalf("loadRuntimeConfig: %v (a kind with no stored value must leave the file's value in effect)", err)
			}
			tt.check(t, b.cfgHolder.Get())
		})
	}
}

// TestDuplicateRepositoriesFailOnTheFilesOwnMessage is the end of the path the
// fix-closure review found. A duplicate [[repos]] entry is refused by the
// repository-policies validator, so the kind is never seeded and stays absent --
// and boot must then fail on the FILE's copy of the value, naming the duplicate,
// with the file as the fix. It used to fail on "control-plane resource not
// found": a database-named error that editing config.toml could not clear.
func TestDuplicateRepositoriesFailOnTheFilesOwnMessage(t *testing.T) {
	resources := databaseOwnedResources()
	delete(resources, controlplane.RepositoryPoliciesKind)
	b := newReloadBoot(t, &controlPlaneStub{values: resources})
	b.cfg.Repos = []config.Repo{{Owner: "acme", Name: "app"}, {Owner: "acme", Name: "app"}}

	err := b.loadRuntimeConfig(t.Context())
	if err == nil {
		t.Fatal("loadRuntimeConfig accepted a duplicate repository list")
	}
	if !errors.Is(err, configuration.ErrInvalidInput) {
		t.Errorf("loadRuntimeConfig = %v, want it to wrap configuration.ErrInvalidInput", err)
	}
	if !strings.Contains(err.Error(), "duplicates") {
		t.Errorf("loadRuntimeConfig = %v, want the duplicate named the way configuration.Validate names it", err)
	}
	if strings.Contains(err.Error(), "not found") {
		t.Errorf("loadRuntimeConfig = %v, want no absent-resource error: the file's value is the one in effect", err)
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
