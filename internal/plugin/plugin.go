// Package plugin defines the core plugin interface for extending archie-core's
// daemon at startup. Plugins are Yaegi-interpreted.go files loaded from
// ~/.config/archie/plugins/ that register themselves with the daemon's
// extension registry.
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

// Plugin is a core plugin loaded by the daemon at startup. Each .go file in
// ~/.config/archie/plugins/ must export a variable named "Plugin" declared
// with an interface type: "var Plugin plugin.Plugin = impl{}". A
// concrete-typed export is refused, because Yaegi only bridges an interpreted
// value to Go as an interface when the declaration names one.
type Plugin interface {
	Name() string
	Version() string
}

// LoadDir discovers and evaluates .go files in the given directory.
// Each file must export a variable named "Plugin" that implements the
// Plugin interface. Failed plugins are logged and skipped  --  the daemon
// starts with the remaining plugins.
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
		p, err := LoadFile(filepath.Join(dir, name), extraSymbols...)
		if err != nil {
			slog.Default().Warn("skipping daemon plugin", "file", name, "err", err)
			continue
		}
		plugins = append(plugins, p)
	}
	return plugins, nil
}

// LoadFile evaluates one plugin file. Each file is package main and needs a
// fresh interpreter to avoid symbol collisions with the other files, so the
// boot load (LoadDir) and a live directory reconciliation both load through
// here rather than through two copies of the interpreter setup.
func LoadFile(path string, extraSymbols ...map[string]map[string]reflect.Value) (Plugin, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin %s: %w", path, err)
	}
	i, err := yaegiutil.New(interp.Options{}, extraSymbols...)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: interpreter setup: %w", path, err)
	}
	p, err := yaegiutil.Resolve[Plugin](i, string(src), "main.Plugin")
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", path, err)
	}
	return p, nil
}
