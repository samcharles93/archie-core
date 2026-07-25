package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/traefik/yaegi/interp"

	"github.com/samcharles93/archie-core/internal/yaegiutil"
)

// Plugin is a discovered Yaegi-interpreted .go file in a skill's plugins/
// directory. The source must be a valid Go package exporting a Run function:
//
//	func Run(input string) string
type Plugin struct {
	Name string // filename without .go extension
	Path string // full path to the .go file
	Src  string // source code content
}

// Run interprets the plugin source via Yaegi and calls its Run function
// with the given input. The plugin runs with full standard library access.
func (p *Plugin) Run(input string) (string, error) {
	i, err := yaegiutil.New(interp.Options{})
	if err != nil {
		return "", fmt.Errorf("plugin %s: %w", p.Name, err)
	}
	// Plugins must use package main — the convention for runnable Yaegi scripts.
	fn, err := yaegiutil.Resolve[func(string) string](i, p.Src, "main.Run")
	if err != nil {
		return "", fmt.Errorf("plugin %s: %w", p.Name, err)
	}
	return fn(input), nil
}

// DiscoverPlugins scans dir/.agents/skills/<skillName>/plugins/*.go and
// returns the discovered plugins sorted by filename. Returns nil if the
// skill directory does not exist. Returns an empty slice if the skill
// exists but has no plugins/ directory.
func DiscoverPlugins(dir, skillName string) ([]Plugin, error) {
	return LoadPlugins(dir, skillName, nil)
}

// LoadPlugins loads plugins from dir/.agents/skills/<skillName>/plugins/.
//
// When allowed is non-empty, only the listed filenames are loaded in
// declared order — the plugins/ prefix is stripped before matching.
// A listed file that does not exist on disk is an error.
//
// When allowed is nil or empty, all *.go files in the plugins/ directory
// are globbed alphabetically (backward-compatible fallback).
func LoadPlugins(dir, skillName string, allowed []string) ([]Plugin, error) {
	pluginsDir := filepath.Join(dir, skillsDir, skillName, "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if os.IsNotExist(err) {
		// Skill exists but has no plugins directory — not an error.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugins dir %s: %w", pluginsDir, err)
	}

	// Build an on-disk set for existence checks when filtering.
	onDisk := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			onDisk[e.Name()] = struct{}{}
		}
	}

	var names []string
	if len(allowed) == 0 {
		// Fallback: glob all *.go files alphabetically.
		for name := range onDisk {
			names = append(names, name)
		}
		sort.Strings(names)
	} else {
		// Use the frontmatter list; strip "plugins/" prefix.
		for _, a := range allowed {
			a = strings.TrimPrefix(a, "plugins/")
			if _, ok := onDisk[a]; !ok {
				return nil, fmt.Errorf("plugin %q listed in metadata.archie.plugins not found on disk", a)
			}
			names = append(names, a)
		}
	}

	plugins := make([]Plugin, 0, len(names))
	for _, name := range names {
		path := filepath.Join(pluginsDir, name)
		src, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read plugin %s: %w", path, err)
		}
		plugins = append(plugins, Plugin{
			Name: strings.TrimSuffix(name, ".go"),
			Path: path,
			Src:  string(src),
		})
	}
	return plugins, nil
}
