package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
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
	i := interp.New(interp.Options{})
	if err := i.Use(stdlib.Symbols); err != nil {
		return "", fmt.Errorf("plugin %s: stdlib: %w", p.Name, err)
	}
	if _, err := i.Eval(p.Src); err != nil {
		return "", fmt.Errorf("plugin %s: eval: %w", p.Name, err)
	}
	// Plugins must use package main — the convention for runnable Yaegi scripts.
	v, err := i.Eval("main.Run")
	if err != nil {
		return "", fmt.Errorf("plugin %s: resolve Run: %w", p.Name, err)
	}
	fn, ok := v.Interface().(func(string) string)
	if !ok {
		return "", fmt.Errorf("plugin %s: Run is %T, want func(string) string", p.Name, v.Interface())
	}
	return fn(input), nil
}

// DiscoverPlugins scans dir/.agents/skills/<skillName>/plugins/*.go and
// returns the discovered plugins sorted by filename. Returns nil if the
// skill directory does not exist. Returns an empty slice if the skill
// exists but has no plugins/ directory.
func DiscoverPlugins(dir, skillName string) ([]Plugin, error) {
	pluginsDir := filepath.Join(dir, skillsDir, skillName, "plugins")
	entries, err := os.ReadDir(pluginsDir)
	if os.IsNotExist(err) {
		// Skill exists but has no plugins directory — not an error.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plugins dir %s: %w", pluginsDir, err)
	}

	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

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
