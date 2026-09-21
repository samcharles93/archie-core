package archied

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/samcharles93/archie-core/internal/app/controlplane"
	"github.com/samcharles93/archie-core/internal/infrastructure/configuration"
)

// TestLoadConfigAcceptsStaleDatabaseOwnedValues is archie-core-i3qm at the boot
// entry point. docs/prds/runtime-control-plane.md, "Bootstrap, migration, and
// recovery": after migration, settings in TOML are ignored and cannot block
// State Store startup. archie-state-store boots through loadConfig and then
// opens the store; it never layers a control-plane resource over the file
// document (state_store.go), so a stale value in a setting the control plane
// owns must not fail boot. Every value below is one a control-plane resource
// replaces wholesale.
func TestLoadConfigAcceptsStaleDatabaseOwnedValues(t *testing.T) {
	tests := []struct {
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

	for _, tt := range tests {
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
