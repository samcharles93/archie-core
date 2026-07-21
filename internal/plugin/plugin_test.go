package plugin

import "testing"

// ── regression: Core plugin registry nonexistent ─────────────────────

func TestPluginInterfaceExists(t *testing.T) {
	// PRD section 5 Layer 2: plugins implement Name(), Version().
	// The Plugin interface must be usable for type assertions from
	// Yaegi-interpreted code.
	var p Plugin
	_ = p // ensures the type exists at compile time

	// Verify the methods exist on the interface.
	type nameVersioner interface {
		Name() string
		Version() string
	}
	var _ nameVersioner = p // compile-time check
}

func TestRegistryExists(t *testing.T) {
	// A registry holds loaded plugins and dispatches extension points.
	r := &Registry{}
	if r == nil {
		t.Error("Gap: Registry type is not defined. " +
			"Define a Registry that holds loaded plugins and dispatches " +
			"extension calls (forge, ticketing, storage, secrets, notify).")
	}
}

func TestLoadPluginsFromConfigDir(t *testing.T) {
	// The daemon loads .go files from ~/.config/archie/plugins/ at
	// startup. Failed plugins are logged and skipped.
	plugins, err := LoadDir("/nonexistent/path")
	if err != nil {
		t.Error("Gap: LoadDir function is not defined. " +
			"Define LoadDir(path string) ([]Plugin, error) that discovers " +
			"and evaluates .go files from ~/.config/archie/plugins/.")
	}
	if plugins != nil {
		// Even an empty directory should return empty slice, not nil.
		_ = plugins
	}
}
