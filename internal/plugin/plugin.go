// Package plugin defines the core plugin interface for extending archie-core's
// daemon at startup. Plugins are Yaegi-interpreted .go files loaded from
// ~/.config/archie/plugins/ that register themselves with the daemon's
// extension registry. PRD section 5 Layer 2.
package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// Plugin is a core plugin loaded by the daemon at startup. Each .go file
// in ~/.config/archie/plugins/ must export a variable named "Plugin" that
// satisfies this interface.
type Plugin interface {
	Name() string
	Version() string
}

// Registry holds loaded plugins and dispatches extension point calls.
type Registry struct {
	plugins []Plugin
}

// Register adds a plugin to the registry.
func (r *Registry) Register(p Plugin) {
	r.plugins = append(r.plugins, p)
}

// Plugins returns all registered plugins.
func (r *Registry) Plugins() []Plugin { return r.plugins }

// LoadDir discovers and evaluates .go files in the given directory.
// Each file must export a variable named "Plugin" that implements the
// Plugin interface. Failed plugins are logged and skipped  --  the daemon
// starts with the remaining plugins (PRD section 5).
//
// extraSymbols are additional Yaegi symbol tables made available to
// interpreted code. Callers should pass pluginextract.Symbols so that
// interpreted types can satisfy the plugin.Plugin interface across
// the Yaegi/Go boundary.
func LoadDir(dir string, extraSymbols ...map[string]map[string]reflect.Value) ([]Plugin, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugin dir %s: %w", dir, err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	plugins := make([]Plugin, 0)
	for _, name := range names {
		path := filepath.Join(dir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			slog.Default().Warn("skipping unreadable daemon plugin", "file", name, "err", err)
			continue
		}
		// Each plugin file is package main  --  must use a fresh interpreter
		// to avoid symbol collisions between files.
		i, err := yaegiutil.New(interp.Options{}, extraSymbols...)
		if err != nil {
			slog.Default().Warn("skipping plugin  --  interpreter setup failed", "file", name, "err", err)
			continue
		}
		p, err := yaegiutil.Resolve[Plugin](i, string(src), "main.Plugin")
		if err != nil {
			slog.Default().Warn("skipping plugin", "file", name, "err", err)
			continue
		}
		plugins = append(plugins, p)
	}
	return plugins, nil
}
