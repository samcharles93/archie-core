package plugin

import "testing"

// ── regression: Core plugin registry nonexistent ─────────────────────

func TestPluginInterfaceExists(t *testing.T) {
	// PRD section 5 Layer 2: core plugins live at ~/.config/archie/plugins/
	// and extend the daemon itself (forges, ticketing, storage, secrets,
	// notifications). Each plugin implements:
	//
	//   type Plugin interface {
	//       Name() string
	//       Version() string
	//       Register(daemon *Daemon) error
	//   }
	//
	// Currently zero code exists — no Plugin interface, no registry,
	// no loader for ~/.config/archie/plugins/.

	var p Plugin
	if p == nil {
		t.Error("Gap: Plugin interface is not defined. " +
			"Define a Plugin interface with Name(), Version(), and Register(*Daemon) error " +
			"methods per PRD section 5 Layer 2.")
	}
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
